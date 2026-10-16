package services

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gperezmz/wakewire/internal/account/application"
	"github.com/gperezmz/wakewire/internal/adapters/httpread"
)

const secret = "tok-SECRET-123"

// serve starts a fake service; routes maps "path?query" (or just the path) to
// a handler, and every request must carry the token one way or another.
func serve(t *testing.T, routes map[string]http.HandlerFunc) (*Prober, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.RequestURI())
		user, pass, basic := r.BasicAuth()
		carried := r.Header.Get("Authorization") == "Bearer "+secret || r.Header.Get("PRIVATE-TOKEN") == secret || (basic && pass == secret && user == "a@b.c")
		if !carried {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		h, ok := routes[r.URL.RequestURI()]
		if !ok {
			h, ok = routes[r.URL.Path]
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	ep := Endpoints{GitHub: srv.URL, Origin: func(string) string { return srv.URL }}
	return New(httpread.New("wakewire-test", nil), ep), &seen
}

func reply(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

func cred(kind application.ServiceKind) application.Credential {
	return application.Credential{Account: "acct", Host: "h.example.com", Service: kind, Token: secret, Email: "a@b.c"}
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	var c interface{ Code() string }
	if !errors.As(err, &c) || c.Code() != code {
		t.Fatalf("want %s, got %v", code, err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatal("the error carries the token")
	}
}

func TestGitHubProbeReadsTheLoginAndTheClassicScopes(t *testing.T) {
	cases := map[string]struct {
		scopes       *string
		known, write bool
	}{
		"repo":         {ptr("gist, repo, read:org"), true, true},
		"public_repo":  {ptr("public_repo"), true, true},
		"read only":    {ptr("read:org"), true, false},
		"empty":        {ptr(""), true, false},
		"fine grained": {nil, false, false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			p, _ := serve(t, map[string]http.HandlerFunc{"/user": func(w http.ResponseWriter, _ *http.Request) {
				if c.scopes != nil {
					w.Header().Set("X-OAuth-Scopes", *c.scopes)
				}
				_, _ = w.Write([]byte(`{"login":"octo","id":583231,"name":"The Octocat"}`))
			}})
			got, err := p.Probe(context.Background(), cred(application.GitHub))
			if err != nil || got.Identity != (application.Identity{Login: "octo", ID: "583231", Name: "The Octocat"}) ||
				got.CanWriteKnown != c.known || got.CanWrite != c.write {
				t.Fatalf("%+v %v", got, err)
			}
		})
	}
}

func ptr(s string) *string { return &s }

func TestGitHubOwnersAndUsers(t *testing.T) {
	p, seen := serve(t, map[string]http.HandlerFunc{
		"/users/acme":    reply(`{"login":"acme","id":1,"type":"Organization"}`),
		"/users/someone": reply(`{"login":"someone","id":42,"name":"Some One"}`),
	})
	c := cred(application.GitHub)
	if err := p.Owner(context.Background(), c, "acme"); err != nil {
		t.Fatal(err)
	}
	wantCode(t, p.Owner(context.Background(), c, "ghost"), "invalid_value")
	id, err := p.Lookup(context.Background(), c, "someone")
	if err != nil || id.Login != "someone" || id.ID != "42" {
		t.Fatalf("%+v %v", id, err)
	}
	_, err = p.Lookup(context.Background(), c, "ghost")
	wantCode(t, err, "invalid_value")
	for _, s := range *seen {
		if !strings.HasPrefix(s, "GET ") {
			t.Fatalf("a probe wrote: %s", s)
		}
	}
}

func TestARefusedTokenIsTokenRefused(t *testing.T) {
	p, _ := serve(t, nil)
	c := cred(application.GitHub)
	c.Token = "wrong-token"
	_, err := p.Probe(context.Background(), c)
	wantCode(t, err, "token_refused")
}

func TestAServerErrorIsUnreachable(t *testing.T) {
	p, _ := serve(t, map[string]http.HandlerFunc{"/user": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(502) }})
	_, err := p.Probe(context.Background(), cred(application.GitHub))
	wantCode(t, err, "unreachable")
}

func TestGitLabProbeAndItsTokenScopes(t *testing.T) {
	for name, c := range map[string]struct {
		self         http.HandlerFunc
		known, write bool
	}{
		"api":       {reply(`{"scopes":["api","read_user"]}`), true, true},
		"read_api":  {reply(`{"scopes":["read_api"]}`), true, false},
		"not a pat": {func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(404) }, false, false},
		"refused":   {func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(403) }, false, false},
	} {
		t.Run(name, func(t *testing.T) {
			p, _ := serve(t, map[string]http.HandlerFunc{
				"/api/v4/user":                        reply(`{"id":9,"username":"gl-me","name":"GL Me"}`),
				"/api/v4/personal_access_tokens/self": c.self,
			})
			got, err := p.Probe(context.Background(), cred(application.GitLab))
			if err != nil || got.Identity.Login != "gl-me" || got.Identity.ID != "9" || got.CanWriteKnown != c.known || got.CanWrite != c.write {
				t.Fatalf("%+v %v", got, err)
			}
		})
	}
}

func TestGitLabOwnersFallBackFromGroupToUser(t *testing.T) {
	p, _ := serve(t, map[string]http.HandlerFunc{
		"/api/v4/groups/platform":      reply(`{"id":3,"path":"platform"}`),
		"/api/v4/users?username=jdoe":  reply(`[{"id":5,"username":"jdoe","name":"G"}]`),
		"/api/v4/users?username=ghost": reply(`[]`),
	})
	c := cred(application.GitLab)
	if err := p.Owner(context.Background(), c, "platform"); err != nil {
		t.Fatal(err)
	}
	if err := p.Owner(context.Background(), c, "jdoe"); err != nil {
		t.Fatal(err)
	}
	wantCode(t, p.Owner(context.Background(), c, "ghost"), "invalid_value")
	id, err := p.Lookup(context.Background(), c, "jdoe")
	if err != nil || id.Login != "jdoe" || id.ID != "5" {
		t.Fatalf("%+v %v", id, err)
	}
	_, err = p.Lookup(context.Background(), c, "ghost")
	wantCode(t, err, "invalid_value")
}

func TestJiraUsesBasicAuthAndTakesAnAccountIDAsGiven(t *testing.T) {
	p, _ := serve(t, map[string]http.HandlerFunc{"/rest/api/3/myself": reply(`{"accountId":"5b10ac8d","displayName":"G Perez"}`)})
	c := cred(application.Jira)
	got, err := p.Probe(context.Background(), c)
	if err != nil || got.Identity != (application.Identity{Login: "5b10ac8d", ID: "5b10ac8d", Name: "G Perez"}) || got.CanWriteKnown {
		t.Fatalf("%+v %v", got, err)
	}
	id, err := p.Lookup(context.Background(), c, "abc123")
	if err != nil || id.Login != "abc123" || id.ID != "abc123" {
		t.Fatalf("%+v %v", id, err)
	}
	if err := p.Owner(context.Background(), c, "ignored"); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultEndpoints(t *testing.T) {
	ep := DefaultEndpoints()
	if ep.GitHub != "https://api.github.com" || ep.Origin("gitlab.client.com") != "https://gitlab.client.com" {
		t.Fatalf("%+v", ep)
	}
}
