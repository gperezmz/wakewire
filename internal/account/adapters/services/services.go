// Package services asks GitHub, GitLab and Jira Cloud whose a token is,
// whether it reads an owner and whether it can write, through the read-only
// client and nothing else.
package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/gperezmz/wakewire/internal/account/application"
	"github.com/gperezmz/wakewire/internal/adapters/httpread"
)

// Endpoints say where each service is, so that tests can point them at a
// local server.
type Endpoints struct {
	GitHub string                   // GitHub's API root
	Origin func(host string) string // scheme and host of a GitLab or Jira host
}

// DefaultEndpoints are the real services: api.github.com, and https on the host.
func DefaultEndpoints() Endpoints {
	return Endpoints{
		GitHub: "https://api.github.com",
		Origin: func(host string) string { return "https://" + host },
	}
}

// Prober is the application's port to the services.
type Prober struct {
	client *httpread.Client
	ep     Endpoints
}

// New returns a prober that reads through client.
func New(client *httpread.Client, ep Endpoints) *Prober { return &Prober{client: client, ep: ep} }

func serviceName(c application.Credential) string {
	switch c.Service {
	case application.GitHub:
		return "GitHub"
	case application.GitLab:
		return "GitLab " + c.Host
	}
	return "Jira " + c.Host
}

// get reads path under the service's API root with the credential's
// authorization. A 404 comes back as a response for the caller to judge; any
// other failure is an error carrying a service code.
func (p *Prober) get(ctx context.Context, c application.Credential, path string) (*httpread.Response, error) {
	var root string
	switch c.Service {
	case application.GitHub:
		root = p.ep.GitHub
	case application.GitLab:
		root = p.ep.Origin(c.Host) + "/api/v4"
	default:
		root = p.ep.Origin(c.Host) + "/rest/api/3"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, root+path, nil)
	if err != nil {
		return nil, httpread.NewError("invalid_value", fmt.Sprintf("%s is not a usable address", root+path), "check `host` of the account in config.toml")
	}
	req.Header.Set("Accept", "application/json")
	switch c.Service {
	case application.GitHub:
		req.Header.Set("Authorization", "Bearer "+c.Token)
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	case application.GitLab:
		req.Header.Set("PRIVATE-TOKEN", c.Token)
	default:
		req.SetBasicAuth(c.Email, c.Token)
	}
	resp, err := p.client.Do(ctx, serviceName(c), req)
	if err != nil {
		return nil, err
	}
	if resp.Status >= 400 && resp.Status != http.StatusNotFound {
		return nil, httpread.NewError("unreachable",
			fmt.Sprintf("%s answered %d to GET %s", serviceName(c), resp.Status, req.URL.Path), "try again; `wakewire doctor` checks every account")
	}
	return resp, nil
}

func decode(c application.Credential, resp *httpread.Response, into any) error {
	if err := json.Unmarshal(resp.Body, into); err != nil {
		return httpread.NewError("unreachable", fmt.Sprintf("%s answered with something wakewire cannot read", serviceName(c)),
			"try again; if it persists, report it with `wakewire doctor`'s output")
	}
	return nil
}

// notFound is a 404 on something the account was told to name.
func notFound(what, flag, line string) error {
	return httpread.NewError("invalid_value", what, fmt.Sprintf("correct %s, or %s", flag, line))
}

type userDoc struct {
	ID       json.Number `json:"id"`
	Login    string      `json:"login"`
	Username string      `json:"username"`
	Name     string      `json:"name"`
}

func (u userDoc) identity() application.Identity {
	login := u.Login
	if login == "" {
		login = u.Username
	}
	return application.Identity{Login: login, ID: u.ID.String(), Name: u.Name}
}

// Probe asks the service whose the token is and whether it can write.
func (p *Prober) Probe(ctx context.Context, c application.Credential) (application.Probe, error) {
	switch c.Service {
	case application.GitHub:
		return p.probeGitHub(ctx, c)
	case application.GitLab:
		return p.probeGitLab(ctx, c)
	}
	return p.probeJira(ctx, c)
}

func (p *Prober) probeGitHub(ctx context.Context, c application.Credential) (application.Probe, error) {
	resp, err := p.get(ctx, c, "/user")
	if err != nil {
		return application.Probe{}, err
	}
	if resp.Status == http.StatusNotFound {
		return application.Probe{}, httpread.NewError("token_refused", "GitHub does not know the token", "replace it with `wakewire account add`")
	}
	var u userDoc
	if err := decode(c, resp, &u); err != nil {
		return application.Probe{}, err
	}
	probe := application.Probe{Identity: u.identity()}
	// Classic tokens list their scopes; fine-grained ones do not, so for
	// those whether they can write is not known from here.
	if scopes, ok := resp.Header["X-Oauth-Scopes"]; ok {
		list := strings.FieldsFunc(strings.Join(scopes, ","), func(r rune) bool { return r == ',' || r == ' ' })
		probe.CanWriteKnown = true
		probe.CanWrite = slices.Contains(list, "repo") || slices.Contains(list, "public_repo")
	}
	return probe, nil
}

func (p *Prober) probeGitLab(ctx context.Context, c application.Credential) (application.Probe, error) {
	resp, err := p.get(ctx, c, "/user")
	if err != nil {
		return application.Probe{}, err
	}
	if resp.Status == http.StatusNotFound {
		return application.Probe{}, httpread.NewError("token_refused", serviceName(c)+" does not know the token", "replace it with `wakewire account add`")
	}
	var u userDoc
	if err := decode(c, resp, &u); err != nil {
		return application.Probe{}, err
	}
	probe := application.Probe{Identity: u.identity()}
	// Only a personal access token can describe itself; for any other kind
	// of token, or an older GitLab, whether it can write stays unknown.
	if self, err := p.get(ctx, c, "/personal_access_tokens/self"); err == nil && self.Status == http.StatusOK {
		var doc struct {
			Scopes []string `json:"scopes"`
		}
		if json.Unmarshal(self.Body, &doc) == nil && len(doc.Scopes) > 0 {
			probe.CanWriteKnown = true
			probe.CanWrite = slices.Contains(doc.Scopes, "api")
		}
	}
	return probe, nil
}

func (p *Prober) probeJira(ctx context.Context, c application.Credential) (application.Probe, error) {
	resp, err := p.get(ctx, c, "/myself")
	if err != nil {
		return application.Probe{}, err
	}
	if resp.Status == http.StatusNotFound {
		return application.Probe{}, httpread.NewError("unreachable", c.Host+" is not a Jira Cloud site", "check `host` of the account in config.toml")
	}
	var doc struct {
		AccountID   string `json:"accountId"`
		DisplayName string `json:"displayName"`
	}
	if err := decode(c, resp, &doc); err != nil {
		return application.Probe{}, err
	}
	return application.Probe{Identity: application.Identity{Login: doc.AccountID, ID: doc.AccountID, Name: doc.DisplayName}}, nil
}

// Lookup finds the user the account reads as: by login on GitHub and GitLab;
// on Jira the accountId is taken as given.
func (p *Prober) Lookup(ctx context.Context, c application.Credential, user string) (application.Identity, error) {
	missing := func() error {
		return notFound(fmt.Sprintf("%s has no user %q", serviceName(c), user), "`--user`", fmt.Sprintf("fix `user` of account %q in config.toml", c.Account))
	}
	switch c.Service {
	case application.GitHub:
		resp, err := p.get(ctx, c, "/users/"+url.PathEscape(user))
		if err != nil {
			return application.Identity{}, err
		}
		if resp.Status == http.StatusNotFound {
			return application.Identity{}, missing()
		}
		var u userDoc
		if err := decode(c, resp, &u); err != nil {
			return application.Identity{}, err
		}
		return u.identity(), nil
	case application.GitLab:
		resp, err := p.get(ctx, c, "/users?username="+url.QueryEscape(user))
		if err != nil {
			return application.Identity{}, err
		}
		var users []userDoc
		if resp.Status == http.StatusNotFound || decode(c, resp, &users) != nil || len(users) == 0 {
			return application.Identity{}, missing()
		}
		return users[0].identity(), nil
	}
	return application.Identity{Login: user, ID: user}, nil
}

// Owner checks that the token reads the organization, group or user.
func (p *Prober) Owner(ctx context.Context, c application.Credential, owner string) error {
	unreadable := notFound(fmt.Sprintf("%s has no organization, group or user %q that this token can read", serviceName(c), owner),
		"`--owner`", fmt.Sprintf("use a token that can read it; the owners of account %q are in config.toml", c.Account))
	switch c.Service {
	case application.GitHub:
		resp, err := p.get(ctx, c, "/users/"+url.PathEscape(owner))
		if err != nil {
			return err
		}
		if resp.Status == http.StatusNotFound {
			return unreadable
		}
	case application.GitLab:
		resp, err := p.get(ctx, c, "/groups/"+url.PathEscape(owner))
		if err != nil {
			return err
		}
		if resp.Status == http.StatusOK {
			return nil
		}
		resp, err = p.get(ctx, c, "/users?username="+url.QueryEscape(owner))
		if err != nil {
			return err
		}
		var users []userDoc
		if resp.Status != http.StatusOK || decode(c, resp, &users) != nil || len(users) == 0 {
			return unreadable
		}
	}
	return nil
}
