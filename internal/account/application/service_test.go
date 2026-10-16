package application

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/gperezmz/wakewire/internal/account/domain"
)

type fakeConfig struct {
	accounts []domain.Account
	err      error
}

func (f *fakeConfig) Accounts() ([]domain.Account, error) {
	return append([]domain.Account{}, f.accounts...), f.err
}
func (f *fakeConfig) Add(a domain.Account) error { f.accounts = append(f.accounts, a); return nil }
func (f *fakeConfig) Rename(o, n string) error {
	for i := range f.accounts {
		if f.accounts[i].Name == o {
			f.accounts[i].Name = n
		}
	}
	return nil
}
func (f *fakeConfig) Remove(name string) error {
	var keep []domain.Account
	for _, a := range f.accounts {
		if a.Name != name {
			keep = append(keep, a)
		}
	}
	f.accounts = keep
	return nil
}

type fakeVault map[string]string

func (v fakeVault) Token(n string) (string, bool, error) { t, ok := v[n]; return t, ok, nil }
func (v fakeVault) Names() ([]string, error) {
	var out []string
	for n := range v {
		out = append(out, n)
	}
	return out, nil
}
func (v fakeVault) Put(n, t string) error { v[n] = t; return nil }
func (v fakeVault) Delete(n string) error { delete(v, n); return nil }

type fakeGH struct {
	tokens map[string]string // login -> token
	asked  []string
}

func (g *fakeGH) Token(host, login string) (string, error) {
	g.asked = append(g.asked, host+"/"+login)
	if t, ok := g.tokens[login]; ok {
		return t, nil
	}
	return "", errors.New("no oauth token found")
}

type fakeProber struct {
	probe       Probe
	probeErr    error
	lookups     []string
	owners      []string
	ownerErr    error
	probeCalls  int
	lookupCalls int
}

func (p *fakeProber) Probe(context.Context, Credential) (Probe, error) {
	p.probeCalls++
	return p.probe, p.probeErr
}
func (p *fakeProber) Lookup(_ context.Context, _ Credential, user string) (Identity, error) {
	p.lookupCalls++
	p.lookups = append(p.lookups, user)
	return Identity{Login: user, ID: "7"}, nil
}
func (p *fakeProber) Owner(_ context.Context, _ Credential, o string) error {
	p.owners = append(p.owners, o)
	return p.ownerErr
}

type fakeCache struct {
	m       map[string]Identity
	forgot  []string
	written []string
}

func (c *fakeCache) Get(a, fp string) (Identity, bool) { id, ok := c.m[a+"|"+fp]; return id, ok }
func (c *fakeCache) Put(a, fp string, id Identity) error {
	c.m[a+"|"+fp] = id
	c.written = append(c.written, a+"|"+fp)
	return nil
}
func (c *fakeCache) Forget(a string) error { c.forgot = append(c.forgot, a); return nil }

type rig struct {
	*Service
	cfg    *fakeConfig
	vault  fakeVault
	gh     *fakeGH
	prober *fakeProber
	cache  *fakeCache
	env    map[string]string
}

func newRig(accounts ...domain.Account) *rig {
	r := &rig{
		cfg: &fakeConfig{accounts: accounts}, vault: fakeVault{}, gh: &fakeGH{tokens: map[string]string{}},
		prober: &fakeProber{probe: Probe{Identity: Identity{Login: "me", ID: "1", Name: "Me"}}},
		cache:  &fakeCache{m: map[string]Identity{}}, env: map[string]string{},
	}
	r.Service = NewService(Ports{Config: r.cfg, Vault: r.vault, GH: r.gh, Prober: r.prober, Cache: r.cache,
		Getenv: func(k string) string { return r.env[k] }})
	return r
}

func ghAccount(name, owner string, src domain.TokenSource) domain.Account {
	return domain.Account{Name: name, Host: "github.com", Owners: []string{owner}, Token: src}
}

type coded interface {
	Code() string
	Hint() string
}

func wantCode(t *testing.T, err error, code string) coded {
	t.Helper()
	var c coded
	if !errors.As(err, &c) || c.Code() != code {
		t.Fatalf("want %s, got %v", code, err)
	}
	if c.Hint() == "" {
		t.Fatalf("%s carries no hint", code)
	}
	return c
}

func TestForRepoResolvesEachTokenSource(t *testing.T) {
	r := newRig(
		ghAccount("work", "ACME", domain.TokenSource{Kind: domain.GH, Arg: "me-work"}),
		ghAccount("env", "ENVORG", domain.TokenSource{Kind: domain.Env, Arg: "MY_TOKEN"}),
		ghAccount("kept", "KEPT", domain.TokenSource{Kind: domain.Stored}),
	)
	r.gh.tokens["me-work"] = "gh-secret"
	r.env["MY_TOKEN"] = " env-secret\n"
	r.vault["kept"] = "stored-secret"
	for owner, want := range map[string]struct{ token, src string }{
		"acme":    {"gh-secret", "gh:me-work"},
		"ENVORG": {"env-secret", "env:MY_TOKEN"},
		"kept":   {"stored-secret", "stored"},
	} {
		c, err := r.ForRepo("github.com", owner)
		if err != nil || c.Token != want.token || c.TokenSource != want.src || c.Service != GitHub {
			t.Errorf("%s: %+v %v", owner, c, err)
		}
	}
	if r.gh.asked[0] != "github.com/me-work" {
		t.Errorf("gh asked %v", r.gh.asked)
	}
}

func TestForRepoErrorsNameTheFix(t *testing.T) {
	r := newRig(
		ghAccount("kept", "KEPT", domain.TokenSource{Kind: domain.Stored}),
		ghAccount("env", "ENVORG", domain.TokenSource{Kind: domain.Env, Arg: "UNSET"}),
		ghAccount("gh", "GHORG", domain.TokenSource{Kind: domain.GH, Arg: "nobody"}),
	)
	_, err := r.ForRepo("github.com", "other")
	if c := wantCode(t, err, "no_account"); !strings.Contains(c.Hint(), "wakewire account add") {
		t.Errorf("hint %q", c.Hint())
	}
	_, err = r.ForRepo("github.com", "kept")
	if c := wantCode(t, err, "no_stored_token"); !strings.Contains(c.Hint(), "wakewire account add kept --host github.com --owner KEPT --token stored") {
		t.Errorf("hint %q", c.Hint())
	}
	_, err = r.ForRepo("github.com", "envorg")
	if c := wantCode(t, err, "no_token"); !strings.Contains(c.Hint(), "UNSET") {
		t.Errorf("hint %q", c.Hint())
	}
	_, err = r.ForRepo("github.com", "ghorg")
	if c := wantCode(t, err, "no_token"); !strings.Contains(c.Hint(), "gh auth login") {
		t.Errorf("hint %q", c.Hint())
	}
}

func TestForSiteFindsTheJiraAccountByHost(t *testing.T) {
	r := newRig(
		ghAccount("work", "ACME", domain.TokenSource{Kind: domain.Stored}),
		domain.Account{Name: "jira", Host: "acme.atlassian.net", Email: "g@n.com", User: "acct-1", Token: domain.TokenSource{Kind: domain.Stored}},
	)
	r.vault["jira"] = "jira-secret"
	c, err := r.ForSite("Acme.Atlassian.net")
	if err != nil || c.Service != Jira || c.Email != "g@n.com" || c.User != "acct-1" || c.Token != "jira-secret" {
		t.Fatalf("%+v %v", c, err)
	}
	_, err = r.ForSite("github.com")
	wantCode(t, err, "no_account")
}

func TestABrokenAccountMakesTheConfigurationInvalid(t *testing.T) {
	r := newRig(domain.Account{Name: "j", Host: "s.atlassian.net", Token: domain.TokenSource{Kind: domain.Stored}})
	_, err := r.ForSite("s.atlassian.net")
	if c := wantCode(t, err, "config_invalid"); !strings.Contains(c.Hint(), "email") {
		t.Errorf("hint %q", c.Hint())
	}
	r.cfg.err = errors.New("boom")
	if _, err := r.List(); err == nil {
		t.Fatal("a store error was swallowed")
	}
}

func TestWhoAmIAsksTheTokenOnceAndCachesByTokenHash(t *testing.T) {
	r := newRig()
	c := Credential{Account: "a", Host: "github.com", Service: GitHub, Token: "tok-1"}
	for range 3 {
		id, err := r.WhoAmI(context.Background(), c)
		if err != nil || id.Login != "me" {
			t.Fatal(id, err)
		}
	}
	if r.prober.probeCalls != 1 {
		t.Fatalf("asked the service %d times", r.prober.probeCalls)
	}
	c.Token = "tok-2"
	if _, err := r.WhoAmI(context.Background(), c); err != nil || r.prober.probeCalls != 2 {
		t.Fatalf("a changed token was not looked up again (%d calls, %v)", r.prober.probeCalls, err)
	}
	for _, k := range r.cache.written {
		if strings.Contains(k, "tok-") {
			t.Fatalf("the cache key holds the token: %q", k)
		}
	}
}

func TestWhoAmIWithAUserOverrideLooksThatUserUp(t *testing.T) {
	r := newRig()
	c := Credential{Account: "a", Host: "github.com", Service: GitHub, Token: "t", User: "someone"}
	id, err := r.WhoAmI(context.Background(), c)
	if err != nil || id.Login != "someone" || r.prober.probeCalls != 0 || r.prober.lookupCalls != 1 {
		t.Fatalf("%+v %v probes=%d lookups=%d", id, err, r.prober.probeCalls, r.prober.lookupCalls)
	}
}

func TestCheckProbesTheTokenAndEachOwner(t *testing.T) {
	r := newRig()
	r.prober.probe.CanWrite, r.prober.probe.CanWriteKnown = true, true
	p, err := r.Check(context.Background(), Credential{Service: GitHub, Token: "t"}, []string{"a", "b"})
	if err != nil || !p.CanWrite || !p.CanWriteKnown || p.Identity.Login != "me" {
		t.Fatalf("%+v %v", p, err)
	}
	if strings.Join(r.prober.owners, ",") != "a,b" {
		t.Fatalf("owners checked: %v", r.prober.owners)
	}
	r.prober.ownerErr = NewError("invalid_value", "owner x not found", "fix --owner")
	if _, err := r.Check(context.Background(), Credential{Service: GitHub}, []string{"x"}); err == nil {
		t.Fatal("an unreadable owner passed")
	}
	r.prober.owners = nil
	r.prober.ownerErr = nil
	if _, err := r.Check(context.Background(), Credential{Service: Jira}, []string{"ignored"}); err != nil || len(r.prober.owners) != 0 {
		t.Fatalf("a Jira site has no owners: %v %v", err, r.prober.owners)
	}
	r.prober.probeErr = NewError("token_refused", "refused", "replace it")
	if _, err := r.Check(context.Background(), Credential{Service: GitHub}, nil); err == nil {
		t.Fatal("a refused token passed")
	}
}

func TestListAndUnusedStoredCarryNoToken(t *testing.T) {
	r := newRig(
		ghAccount("a", "X", domain.TokenSource{Kind: domain.Stored}),
		domain.Account{Name: "j", Host: "s.atlassian.net", Email: "e@x.y", Token: domain.TokenSource{Kind: domain.Stored}},
	)
	r.vault["a"], r.vault["old"], r.vault["older"] = "SECRET-A", "SECRET-OLD", "SECRET-OLDER"
	views, err := r.List()
	if err != nil || len(views) != 2 || views[1].Service != Jira || views[1].Owners == nil || views[0].TokenSource != "stored" {
		t.Fatalf("%+v %v", views, err)
	}
	unused, err := r.UnusedStored()
	sort.Strings(unused)
	if err != nil || strings.Join(unused, ",") != "old,older" {
		t.Fatalf("%v %v", unused, err)
	}
}
