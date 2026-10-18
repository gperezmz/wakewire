package application

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gperezmz/wakewire/internal/account/domain"
)

func addReq() AddRequest {
	return AddRequest{Name: "work", Host: "GitHub.com", Owners: []string{"ACME", "acme", " "}, Token: "s3cret\n"}
}

func TestAddChecksTheTokenThenWritesBothFiles(t *testing.T) {
	r := newRig()
	r.prober.probe.CanWriteKnown = true
	res, err := r.Add(context.Background(), addReq())
	if err != nil || res.Outcome != OutcomeAdded || res.Identity.Login != "me" || res.CanWrite == nil || *res.CanWrite {
		t.Fatalf("%+v %v", res, err)
	}
	if len(r.cfg.accounts) != 1 || r.cfg.accounts[0].Host != "github.com" || len(r.cfg.accounts[0].Owners) != 1 {
		t.Fatalf("config: %+v", r.cfg.accounts)
	}
	if r.vault["work"] != "s3cret" {
		t.Fatal("token not stored (trimmed)")
	}
	if strings.Join(r.prober.owners, ",") != "ACME" {
		t.Fatalf("owners checked %v", r.prober.owners)
	}
}

func TestAddWritesNothingWhenTheCheckFails(t *testing.T) {
	r := newRig()
	r.prober.probeErr = NewError("token_refused", "GitHub refused the token (401)", "replace it")
	_, err := r.Add(context.Background(), addReq())
	wantCode(t, err, "token_refused")
	if len(r.cfg.accounts) != 0 || len(r.vault) != 0 {
		t.Fatalf("wrote after a refused token: %v %v", r.cfg.accounts, r.vault)
	}
}

func TestAddDryRunChecksButWritesNothing(t *testing.T) {
	r := newRig()
	req := addReq()
	req.DryRun = true
	res, err := r.Add(context.Background(), req)
	if err != nil || !res.DryRun || res.Outcome != OutcomeAdded || res.Identity == nil {
		t.Fatalf("%+v %v", res, err)
	}
	if len(r.cfg.accounts) != 0 || len(r.vault) != 0 {
		t.Fatal("a dry run wrote")
	}
}

func TestAddingTheSameAccountAgainIsAlreadyDone(t *testing.T) {
	r := newRig()
	if _, err := r.Add(context.Background(), addReq()); err != nil {
		t.Fatal(err)
	}
	calls := r.prober.probeCalls
	res, err := r.Add(context.Background(), addReq())
	if err != nil || res.Outcome != OutcomeAlready || res.Identity != nil {
		t.Fatalf("%+v %v", res, err)
	}
	if r.prober.probeCalls != calls || len(r.cfg.accounts) != 1 {
		t.Fatal("the repeat did work")
	}
}

func TestAddingAnExistingAccountWithAnotherStoredTokenReplacesIt(t *testing.T) {
	r := newRig()
	if _, err := r.Add(context.Background(), addReq()); err != nil {
		t.Fatal(err)
	}
	req := addReq()
	req.Token = "newer"
	res, err := r.Add(context.Background(), req)
	if err != nil || res.Outcome != OutcomeReplaced || r.vault["work"] != "newer" || len(r.cfg.accounts) != 1 {
		t.Fatalf("%+v %v %v", res, err, r.vault)
	}
	delete(r.vault, "work")
	res, err = r.Add(context.Background(), req)
	if err != nil || res.Outcome != OutcomeStored || r.vault["work"] != "newer" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestAddRefusesWhatClashes(t *testing.T) {
	r := newRig(ghAccount("work", "ACME", domain.TokenSource{Kind: domain.GH, Arg: "x"}))
	req := addReq() // same name, different token source
	_, err := r.Add(context.Background(), req)
	wantCode(t, err, "account_exists")

	req.Name = "other"
	_, err = r.Add(context.Background(), req)
	if c := wantCode(t, err, "account_conflict"); !strings.Contains(c.Hint(), "wakewire account remove work") {
		t.Errorf("hint %q", c.Hint())
	}
}

func TestAddWithEnvAndGHSourcesResolvesTheTokenAndStoresNothing(t *testing.T) {
	r := newRig()
	r.env["MY_VAR"] = "from-env"
	res, err := r.Add(context.Background(), envReq())
	if err != nil || res.Outcome != OutcomeAdded || len(r.vault) != 0 || r.cfg.accounts[0].Token.String() != "env:MY_VAR" {
		t.Fatalf("%+v %v", res, err)
	}
	_, err = r.Add(context.Background(), AddRequest{Name: "g", Host: "github.com", Owners: []string{"q"}, TokenSource: "gh:nobody"})
	wantCode(t, err, "no_token")
	res, err = r.Add(context.Background(), envReq())
	if err != nil || res.Outcome != OutcomeAlready {
		t.Fatalf("%+v %v", res, err)
	}
}

func envReq() AddRequest {
	return AddRequest{Name: "e", Host: "gitlab.x.com", Owners: []string{"p"}, TokenSource: "env:MY_VAR"} //nolint:gosec // an environment variable's name, not a secret
}

// A token typed where a token source belongs must not come back in the error.
func TestAddNeverEchoesATokenGivenAsTheTokenSource(t *testing.T) {
	const secret = "ghp_SECRETVALUE0123456789" //nolint:gosec // a made-up value, to see it never comes back
	for _, src := range []string{secret, "env:" + secret, "gh:" + secret} {
		r := newRig()
		_, err := r.Add(context.Background(), AddRequest{Name: "a", Host: "github.com", Owners: []string{"o"}, TokenSource: src})
		if err == nil {
			t.Fatalf("%s: want an error", src)
		}
		var c coded
		if !errors.As(err, &c) {
			t.Fatalf("%s: %v is not coded", src, err)
		}
		if text := err.Error() + c.Hint(); strings.Contains(text, "SECRET") {
			t.Errorf("%s: error quotes the value: %s", src, text)
		}
	}
}

func TestAddRejectsBadInputAsInvalidValue(t *testing.T) {
	cases := map[string]AddRequest{
		"name":          {Name: "a b", Host: "github.com", Owners: []string{"o"}, Token: "t"},
		"no owner":      {Name: "a", Host: "github.com", Token: "t"},
		"jira no email": {Name: "a", Host: "s.atlassian.net", Token: "t"},
		"bad source":    {Name: "a", Host: "github.com", Owners: []string{"o"}, TokenSource: "file:x", Token: "t"},
		"no token":      {Name: "a", Host: "github.com", Owners: []string{"o"}},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRig()
			_, err := r.Add(context.Background(), req)
			wantCode(t, err, "invalid_value")
		})
	}
}

func TestAddAJiraAccountChecksNoOwners(t *testing.T) {
	r := newRig()
	res, err := r.Add(context.Background(), AddRequest{Name: "jira", Host: "s.atlassian.net", Email: "a@b.c", Token: "t"})
	if err != nil || res.Outcome != OutcomeAdded || len(r.prober.owners) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestRenameMovesTheConfigAndTheStoredTokenTogether(t *testing.T) {
	r := newRig(ghAccount("old", "X", domain.TokenSource{Kind: domain.Stored}))
	r.vault["old"] = "tok"
	res, err := r.Rename("old", "new", true)
	if err != nil || res.Outcome != OutcomeRenamed || !res.DryRun || r.cfg.accounts[0].Name != "old" || r.vault["old"] != "tok" {
		t.Fatalf("dry run: %+v %v", res, err)
	}
	res, err = r.Rename("old", "new", false)
	if err != nil || !res.TokenMoved || r.cfg.accounts[0].Name != "new" || r.vault["new"] != "tok" {
		t.Fatalf("%+v %v", res, err)
	}
	if _, stale := r.vault["old"]; stale || len(r.cache.forgot) != 1 {
		t.Fatalf("old entry kept or cache not forgotten: %v %v", r.vault, r.cache.forgot)
	}
	res, err = r.Rename("old", "new", false)
	if err != nil || res.Outcome != OutcomeAlready {
		t.Fatalf("repeat: %+v %v", res, err)
	}
}

func TestRenameRefusals(t *testing.T) {
	r := newRig(ghAccount("a", "X", domain.TokenSource{Kind: domain.Stored}), ghAccount("b", "Y", domain.TokenSource{Kind: domain.Stored}))
	_, err := r.Rename("a", "b", false)
	wantCode(t, err, "account_exists")
	_, err = r.Rename("nope", "c", false)
	wantCode(t, err, "no_such_account")
	_, err = r.Rename("a", "bad name", false)
	wantCode(t, err, "invalid_value")
}

func TestRemoveDeletesTheAccountAndItsToken(t *testing.T) {
	r := newRig(ghAccount("a", "X", domain.TokenSource{Kind: domain.Stored}))
	r.vault["a"] = "tok"
	res, err := r.Remove("a", true)
	if err != nil || !res.DryRun || len(r.cfg.accounts) != 1 || len(r.vault) != 1 {
		t.Fatalf("dry run: %+v %v", res, err)
	}
	res, err = r.Remove("a", false)
	if err != nil || res.Outcome != OutcomeRemoved || !res.AccountRemoved || !res.TokenRemoved || len(r.cfg.accounts) != 0 || len(r.vault) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	res, err = r.Remove("a", false)
	if err != nil || res.Outcome != OutcomeAbsent {
		t.Fatalf("repeat: %+v %v", res, err)
	}
}

func TestRemoveDeletesAnOrphanedStoredToken(t *testing.T) {
	r := newRig()
	r.vault["ghost"] = "tok"
	res, err := r.Remove("ghost", false)
	if err != nil || res.AccountRemoved || !res.TokenRemoved || len(r.vault) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
}

// emptyAware is a vault as credentials.toml is: an entry holding an empty
// token is listed in Names, but Token says there is no token.
type emptyAware struct{ fakeVault }

func (v emptyAware) Token(n string) (string, bool, error) {
	t, ok := v.fakeVault[n]
	return t, ok && t != "", nil
}

// An entry with an empty token is one doctor reports as unused, so remove
// must be able to delete it, and says a stored token went.
func TestRemoveDeletesAnEntryWhoseTokenIsEmpty(t *testing.T) {
	r := newRig()
	r.vault["ghost"] = ""
	svc := NewService(Ports{Config: r.cfg, Vault: emptyAware{r.vault}, GH: r.gh, Prober: r.prober, Cache: r.cache,
		Getenv: func(k string) string { return r.env[k] }})
	if unused, _ := svc.UnusedStored(); len(unused) != 1 || unused[0] != "ghost" {
		t.Fatalf("doctor does not see the entry: %v", unused)
	}
	res, err := svc.Remove("ghost", true)
	if err != nil || res.Outcome != OutcomeRemoved || !res.TokenRemoved || !res.DryRun || len(r.vault) != 1 {
		t.Fatalf("dry run: %+v %v", res, err)
	}
	res, err = svc.Remove("ghost", false)
	if err != nil || res.Outcome != OutcomeRemoved || res.AccountRemoved || !res.TokenRemoved || len(r.vault) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	if res, err = svc.Remove("ghost", false); err != nil || res.Outcome != OutcomeAbsent {
		t.Fatalf("repeat: %+v %v", res, err)
	}
}
