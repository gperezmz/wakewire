// Package application is what the rest of wakewire asks of accounts: the
// credential that reads a repository or a Jira site, whose it is, whether it
// can write, and the commands that add, rename and remove accounts.
package application

import (
	"context"
	"fmt"

	"github.com/gperezmz/wakewire/internal/account/domain"
)

// ServiceKind is the service an account's host is: "github", "gitlab" or "jira".
type ServiceKind = domain.ServiceKind

// The services wakewire reads.
const (
	GitHub = domain.GitHub
	GitLab = domain.GitLab
	Jira   = domain.Jira
)

// KindForHost: "github.com" is GitHub, a host ending ".atlassian.net" is
// Jira, any other host is GitLab.
func KindForHost(host string) ServiceKind { return domain.KindForHost(host) }

// Credential is everything needed to call one service as one account.
type Credential struct {
	Account     string // the account's name
	Host        string
	Service     ServiceKind
	Token       string // never printed, logged or put in an error
	TokenSource string // "stored", "env:VAR", "gh:login"
	User        string // identity override from config, "" when unset
	Email       string // Jira only
}

// Identity is whose a token is, or who an account is told to read as.
type Identity struct {
	Login string `json:"login"` // GitHub/GitLab login; Jira: the accountId
	ID    string `json:"id"`    // GitHub numeric id, GitLab numeric id, Jira accountId
	Name  string `json:"name"`  // display name where the service gives one
}

// Probe is what checking a token learned.
type Probe struct {
	Identity      Identity
	CanWrite      bool
	CanWriteKnown bool
}

// AccountView is an account as `account list` shows it. It holds no token.
type AccountView struct {
	Name        string      `json:"name"`
	Host        string      `json:"host"`
	Service     ServiceKind `json:"service"`
	Owners      []string    `json:"owners"`
	Email       string      `json:"email,omitempty"`
	User        string      `json:"user,omitempty"`
	TokenSource string      `json:"token"`
}

func view(a domain.Account) AccountView {
	owners := a.Owners
	if owners == nil {
		owners = []string{}
	}
	return AccountView{Name: a.Name, Host: a.Host, Service: a.Service(), Owners: owners,
		Email: a.Email, User: a.User, TokenSource: a.Token.String()}
}

// Error is a refusal carrying the CLI's error code and a hint naming the
// command or configuration line that fixes it.
type Error struct{ code, message, hint string }

func (e *Error) Error() string { return e.message }
func (e *Error) Code() string  { return e.code }
func (e *Error) Hint() string  { return e.hint }

// NewError builds a coded error.
func NewError(code, message, hint string) *Error {
	return &Error{code: code, message: message, hint: hint}
}

// Error codes this context adds to the CLI's table (docs/reference/cli.md).
const (
	CodeNoAccount     = "no_account"
	CodeNoStoredToken = "no_stored_token"
	CodeNoToken       = "no_token"
	CodeConfigInvalid = "config_invalid"
	CodeInvalidValue  = "invalid_value"
	CodeAccountExists = "account_exists"
	CodeAccountClash  = "account_conflict"
	CodeNoSuchAccount = "no_such_account"
)

// ConfigStore is config.toml's accounts. Its errors are config_invalid.
type ConfigStore interface {
	Accounts() ([]domain.Account, error)
	Add(domain.Account) error
	Rename(oldName, newName string) error
	Remove(name string) error
}

// Vault is credentials.toml, by account name. Its errors are config_invalid.
type Vault interface {
	Token(name string) (token string, found bool, err error)
	Names() ([]string, error)
	Put(name, token string) error
	Delete(name string) error
}

// GHTokens asks the gh command for a login's token on a host.
type GHTokens interface {
	Token(host, login string) (string, error)
}

// Prober asks the services. Its errors carry the service codes.
type Prober interface {
	// Probe checks the credential's token: whose it is and, where the
	// service says, whether it can write.
	Probe(ctx context.Context, c Credential) (Probe, error)
	// Lookup finds a user by login (GitHub, GitLab) or takes an accountId (Jira).
	Lookup(ctx context.Context, c Credential, user string) (Identity, error)
	// Owner checks that the token can read the organization, group or user.
	Owner(ctx context.Context, c Credential, owner string) error
}

// IdentityCache keeps looked-up identities, by account and a fingerprint of
// what the lookup depended on, so a changed token is looked up again.
type IdentityCache interface {
	Get(account, fingerprint string) (Identity, bool)
	Put(account, fingerprint string, id Identity) error
	Forget(account string) error
}

// Ports is everything a Service reaches outside the process through.
type Ports struct {
	Config ConfigStore
	Vault  Vault
	GH     GHTokens
	Prober Prober
	Cache  IdentityCache
	Getenv func(string) string
}

func addCommand(a domain.Account, tokenFlag string) string {
	s := fmt.Sprintf("wakewire account add %s --host %s", a.Name, a.Host)
	for _, o := range a.Owners {
		s += " --owner " + o
	}
	if a.Email != "" {
		s += " --email " + a.Email
	}
	if a.User != "" {
		s += " --user " + a.User
	}
	return s + " --token " + tokenFlag
}
