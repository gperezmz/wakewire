// Package domain holds the rules of accounts: what makes one valid, which
// service a host means, where its token comes from and which account reads a
// repository or a Jira site.
package domain

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// ServiceKind is the service an account's host is.
type ServiceKind string

// The services wakewire reads.
const (
	GitHub ServiceKind = "github"
	GitLab ServiceKind = "gitlab"
	Jira   ServiceKind = "jira"
)

// KindForHost: github.com is GitHub, a host ending .atlassian.net is a Jira
// Cloud site, and any other host is a GitLab.
func KindForHost(host string) ServiceKind {
	h := strings.ToLower(strings.TrimSpace(host))
	switch {
	case h == "github.com":
		return GitHub
	case strings.HasSuffix(h, ".atlassian.net"):
		return Jira
	}
	return GitLab
}

// SourceKind is where a token comes from.
type SourceKind string

// The three token sources.
const (
	Stored SourceKind = "stored"
	Env    SourceKind = "env"
	GH     SourceKind = "gh"
)

// TokenSource says where an account's token comes from: stored in
// credentials.toml, in the environment variable Arg, or from gh's login Arg.
type TokenSource struct {
	Kind SourceKind
	Arg  string
}

func (t TokenSource) String() string {
	if t.Kind == Stored {
		return "stored"
	}
	return string(t.Kind) + ":" + t.Arg
}

// ParseTokenSource reads "stored", "env:<VAR>" or "gh:<login>".
func ParseTokenSource(s string) (TokenSource, error) {
	switch {
	case s == "stored":
		return TokenSource{Kind: Stored}, nil
	case strings.HasPrefix(s, "env:") && validVar.MatchString(s[4:]):
		return TokenSource{Kind: Env, Arg: s[4:]}, nil
	case strings.HasPrefix(s, "gh:") && s[3:] != "" && !strings.ContainsAny(s[3:], " \t\n"):
		return TokenSource{Kind: GH, Arg: s[3:]}, nil
	}
	return TokenSource{}, &Violation{Field: "token", Message: fmt.Sprintf("%q is not a token source: use stored, env:<VAR> or gh:<login>", s)}
}

var (
	validVar  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
)

// Violation is a rule an account breaks: Field is the key it breaks it on.
type Violation struct {
	Field   string
	Message string
}

func (v *Violation) Error() string { return v.Message }

// ValidName says whether a new account may be called name: letters, digits,
// hyphens and underscores, starting with a letter or digit, up to 64.
func ValidName(name string) error {
	if !validName.MatchString(name) {
		return &Violation{Field: "name", Message: fmt.Sprintf("%q is not an account name: use letters, digits, - and _, up to 64, starting with a letter or digit", name)}
	}
	return nil
}

// Account pairs a host, the owners it covers and a token source with an
// optional identity override.
type Account struct {
	Name   string
	Host   string
	Owners []string
	Email  string
	User   string
	Token  TokenSource
}

// Service is the service the account's host is.
func (a Account) Service() ServiceKind { return KindForHost(a.Host) }

// Validate checks the rules a single account keeps; a name is only checked
// for being present, so an account somebody wrote by hand still loads.
func (a Account) Validate() error {
	if a.Name == "" {
		return &Violation{Field: "name", Message: "an account needs a name"}
	}
	if a.Host == "" || strings.ContainsAny(a.Host, "/ \t:") {
		return &Violation{Field: "host", Message: fmt.Sprintf("%q is not a host: write it as github.com, gitlab.example.com or a .atlassian.net site", a.Host)}
	}
	for _, o := range a.Owners {
		if strings.TrimSpace(o) == "" || strings.ContainsAny(o, " \t") {
			return &Violation{Field: "owners", Message: fmt.Sprintf("%q is not an owner", o)}
		}
	}
	if a.Token.Kind == "" {
		return &Violation{Field: "token", Message: "the token source is missing or not one of stored, env:<VAR>, gh:<login>"}
	}
	if a.Service() == Jira {
		if len(a.Owners) > 0 {
			return &Violation{Field: "owners", Message: "a Jira account has no owners: its host is the site it covers"}
		}
		if a.Email == "" {
			return &Violation{Field: "email", Message: "a Jira account needs the email its API token belongs to"}
		}
	} else {
		if len(a.Owners) == 0 {
			return &Violation{Field: "owners", Message: "a GitHub or GitLab account needs at least one owner"}
		}
		if a.Email != "" {
			return &Violation{Field: "email", Message: "only a Jira account has an email"}
		}
	}
	return nil
}

// Covers says whether a reads repositories of owner on host.
func (a Account) Covers(host, owner string) bool {
	return a.Service() != Jira && strings.EqualFold(a.Host, host) &&
		slices.ContainsFunc(a.Owners, func(o string) bool { return strings.EqualFold(o, owner) })
}

// CoversSite says whether a is the Jira account of the site.
func (a Account) CoversSite(host string) bool {
	return a.Service() == Jira && strings.EqualFold(a.Host, host)
}

// Same says whether two accounts are written alike, owners in any order.
func (a Account) Same(b Account) bool {
	norm := func(os []string) []string {
		out := make([]string, len(os))
		for i, o := range os {
			out[i] = strings.ToLower(o)
		}
		slices.Sort(out)
		return out
	}
	return a.Name == b.Name && strings.EqualFold(a.Host, b.Host) && a.Email == b.Email &&
		a.User == b.User && a.Token == b.Token && slices.Equal(norm(a.Owners), norm(b.Owners))
}

// Conflict finds an account other than candidate's namesake that covers the
// same host and owner, or the same Jira site, as candidate.
func Conflict(accounts []Account, candidate Account) (Account, bool) {
	for _, other := range accounts {
		if other.Name == candidate.Name {
			continue
		}
		if candidate.Service() == Jira && other.CoversSite(candidate.Host) {
			return other, true
		}
		for _, o := range candidate.Owners {
			if other.Covers(candidate.Host, o) {
				return other, true
			}
		}
	}
	return Account{}, false
}

// Find returns the account called name.
func Find(accounts []Account, name string) (Account, bool) {
	i := slices.IndexFunc(accounts, func(a Account) bool { return a.Name == name })
	if i < 0 {
		return Account{}, false
	}
	return accounts[i], true
}

// ForRepo is the account whose host matches and whose owners list owner.
func ForRepo(accounts []Account, host, owner string) (Account, bool) {
	i := slices.IndexFunc(accounts, func(a Account) bool { return a.Covers(host, owner) })
	if i < 0 {
		return Account{}, false
	}
	return accounts[i], true
}

// ForSite is the Jira account whose host is the site.
func ForSite(accounts []Account, host string) (Account, bool) {
	i := slices.IndexFunc(accounts, func(a Account) bool { return a.CoversSite(host) })
	if i < 0 {
		return Account{}, false
	}
	return accounts[i], true
}
