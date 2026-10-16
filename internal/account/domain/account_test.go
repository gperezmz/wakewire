package domain

import (
	"errors"
	"testing"
)

func TestKindForHost(t *testing.T) {
	cases := map[string]ServiceKind{
		"github.com":         GitHub,
		"GitHub.com":         GitHub,
		"acme.atlassian.net": Jira,
		"atlassian.net":      GitLab, // no site name: not a Jira Cloud site
		"gitlab.com":         GitLab,
		"gitlab.client.com":  GitLab,
	}
	for host, want := range cases {
		if got := KindForHost(host); got != want {
			t.Errorf("%s: got %s want %s", host, got, want)
		}
	}
}

func TestParseTokenSource(t *testing.T) {
	good := map[string]TokenSource{
		"stored":       {Kind: Stored},
		"env:MY_TOKEN": {Kind: Env, Arg: "MY_TOKEN"},
		"gh:jdoe-neo":  {Kind: GH, Arg: "jdoe-neo"},
	}
	for in, want := range good {
		got, err := ParseTokenSource(in)
		if err != nil || got != want || got.String() != in {
			t.Errorf("%s: %v %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "env:", "env:1X", "env:A B", "gh:", "file:/x", "Stored", "gh:a b"} {
		var v *Violation
		if _, err := ParseTokenSource(bad); !errors.As(err, &v) || v.Field != "token" {
			t.Errorf("%q: got %v", bad, err)
		}
	}
}

func TestValidName(t *testing.T) {
	for _, ok := range []string{"a", "work-github", "client_gitlab2"} {
		if ValidName(ok) != nil {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "-a", "a b", "a.b", "a]", `a"`, "é"} {
		if ValidName(bad) == nil {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func TestValidate(t *testing.T) {
	forge := Account{Name: "a", Host: "github.com", Owners: []string{"x"}, Token: TokenSource{Kind: Stored}}
	jira := Account{Name: "j", Host: "s.atlassian.net", Email: "a@b.c", Token: TokenSource{Kind: Stored}}
	if forge.Validate() != nil || jira.Validate() != nil {
		t.Fatal("valid accounts refused")
	}
	cases := []struct {
		field string
		mod   func(Account) Account
		base  Account
	}{
		{"owners", func(a Account) Account { a.Owners = nil; return a }, forge},
		{"email", func(a Account) Account { a.Email = "a@b.c"; return a }, forge},
		{"host", func(a Account) Account { a.Host = "https://github.com"; return a }, forge},
		{"owners", func(a Account) Account { a.Owners = []string{"o"}; return a }, jira},
		{"email", func(a Account) Account { a.Email = ""; return a }, jira},
		{"name", func(a Account) Account { a.Name = ""; return a }, jira},
		{"token", func(a Account) Account { a.Token = TokenSource{}; return a }, jira},
	}
	for i, c := range cases {
		var v *Violation
		if err := c.mod(c.base).Validate(); !errors.As(err, &v) || v.Field != c.field {
			t.Errorf("case %d: got %v want field %s", i, err, c.field)
		}
	}
}

func TestChoosingAnAccount(t *testing.T) {
	gh := Account{Name: "gh", Host: "github.com", Owners: []string{"acme"}}
	me := Account{Name: "me", Host: "github.com", Owners: []string{"gperezmz"}}
	jira := Account{Name: "j", Host: "acme.atlassian.net", Email: "a@b.c"}
	all := []Account{gh, me, jira}
	if a, ok := ForRepo(all, "GitHub.com", "ACME"); !ok || a.Name != "gh" {
		t.Errorf("by owner, any case: %v %v", a, ok)
	}
	if _, ok := ForRepo(all, "github.com", "other"); ok {
		t.Error("an owner no account lists matched")
	}
	if _, ok := ForRepo(all, "acme.atlassian.net", "x"); ok {
		t.Error("a Jira account read a repository")
	}
	if a, ok := ForSite(all, "acme.atlassian.net"); !ok || a.Name != "j" {
		t.Errorf("site: %v %v", a, ok)
	}
	if _, ok := ForSite(all, "github.com"); ok {
		t.Error("a forge account served a Jira site")
	}
}

func TestConflictAndSame(t *testing.T) {
	gh := Account{Name: "gh", Host: "github.com", Owners: []string{"a", "b"}, Token: TokenSource{Kind: Stored}}
	jira := Account{Name: "j", Host: "s.atlassian.net", Email: "e@x.y"}
	all := []Account{gh, jira}
	if c, ok := Conflict(all, Account{Name: "new", Host: "github.com", Owners: []string{"B"}}); !ok || c.Name != "gh" {
		t.Errorf("owner clash: %v %v", c, ok)
	}
	if _, ok := Conflict(all, Account{Name: "new", Host: "github.com", Owners: []string{"c"}}); ok {
		t.Error("no clash expected")
	}
	if _, ok := Conflict(all, Account{Name: "gh", Host: "github.com", Owners: []string{"a"}}); ok {
		t.Error("an account does not conflict with itself")
	}
	if c, ok := Conflict(all, Account{Name: "j2", Host: "s.atlassian.net", Email: "e@x.y"}); !ok || c.Name != "j" {
		t.Errorf("site clash: %v %v", c, ok)
	}
	again := gh
	again.Owners = []string{"B", "a"}
	if !gh.Same(again) {
		t.Error("owner order or case should not matter")
	}
	again.Token = TokenSource{Kind: Env, Arg: "X"}
	if gh.Same(again) {
		t.Error("a different token source is a different account")
	}
}
