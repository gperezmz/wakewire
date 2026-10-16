package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/gperezmz/wakewire/internal/account/domain"
)

// Service answers which account reads what, with which token, and as whom.
type Service struct {
	p Ports
}

// NewService builds a Service over its ports.
func NewService(p Ports) *Service {
	if p.Getenv == nil {
		p.Getenv = func(string) string { return "" }
	}
	return &Service{p: p}
}

// accounts loads every account and refuses a configuration any of them
// breaks, since wakewire never guesses what an account meant.
func (s *Service) accounts() ([]domain.Account, error) {
	accounts, err := s.p.Config.Accounts()
	if err != nil {
		return nil, err
	}
	for _, a := range accounts {
		if err := a.Validate(); err != nil {
			field := "name"
			if v, ok := errors.AsType[*domain.Violation](err); ok {
				field = v.Field
			}
			return nil, NewError(CodeConfigInvalid, fmt.Sprintf("account %q: %s", a.Name, err),
				fmt.Sprintf("fix `%s` in the [[accounts]] block named %q in config.toml", field, a.Name))
		}
	}
	return accounts, nil
}

// ForRepo is the credential that reads <host>/<owner>/<repo>: the account
// whose host matches and whose owners list owner.
func (s *Service) ForRepo(host, owner string) (Credential, error) {
	accounts, err := s.accounts()
	if err != nil {
		return Credential{}, err
	}
	a, ok := domain.ForRepo(accounts, host, owner)
	if !ok {
		return Credential{}, NewError(CodeNoAccount,
			fmt.Sprintf("no account covers %s/%s", host, owner),
			fmt.Sprintf("add one: `wakewire account add <name> --host %s --owner %s --token gh:<login>` (or env:<VAR>, or stored)", host, owner))
	}
	return s.credential(a)
}

// ForSite is the credential for a Jira site: the account whose host it is.
func (s *Service) ForSite(host string) (Credential, error) {
	accounts, err := s.accounts()
	if err != nil {
		return Credential{}, err
	}
	a, ok := domain.ForSite(accounts, host)
	if !ok {
		return Credential{}, NewError(CodeNoAccount,
			fmt.Sprintf("no account covers the Jira site %s", host),
			fmt.Sprintf("add one: `wakewire account add <name> --host %s --email <email>` and give the API token", host))
	}
	return s.credential(a)
}

func (s *Service) credential(a domain.Account) (Credential, error) {
	token, err := s.token(a)
	if err != nil {
		return Credential{}, err
	}
	return Credential{
		Account: a.Name, Host: a.Host, Service: a.Service(), Token: token,
		TokenSource: a.Token.String(), User: a.User, Email: a.Email,
	}, nil
}

// token resolves where an account says its token comes from.
func (s *Service) token(a domain.Account) (string, error) {
	return s.resolve(a, true)
}

// resolve reads the token a.Token names. With named false its errors leave
// out the variable or login the source names: a source typed on the command
// line may be a token pasted in the wrong place, and no token is ever printed.
func (s *Service) resolve(a domain.Account, named bool) (string, error) {
	switch a.Token.Kind {
	case domain.Stored:
		t, found, err := s.p.Vault.Token(a.Name)
		if err != nil {
			return "", err
		}
		if !found {
			return "", NewError(CodeNoStoredToken,
				fmt.Sprintf("account %q says token = \"stored\" but credentials.toml has no entry for it", a.Name),
				fmt.Sprintf("store one: `%s` (the token is read from the prompt or stdin); or change `token` in config.toml", addCommand(a, "stored")))
		}
		return t, nil
	case domain.Env:
		t := strings.TrimSpace(s.p.Getenv(a.Token.Arg))
		if t == "" && !named {
			return "", NewError(CodeNoToken,
				fmt.Sprintf("account %q reads its token from an environment variable, which is empty or unset", a.Name),
				"export that variable where wakewire runs, or give another --token source")
		}
		if t == "" {
			return "", NewError(CodeNoToken,
				fmt.Sprintf("account %q reads its token from $%s, which is empty or unset", a.Name, a.Token.Arg),
				fmt.Sprintf("export %s where wakewire runs, or change `token` of account %q in config.toml", a.Token.Arg, a.Name))
		}
		return t, nil
	case domain.GH:
		t, err := s.p.GH.Token(a.Host, a.Token.Arg)
		if err != nil && !named {
			return "", NewError(CodeNoToken,
				fmt.Sprintf("gh gave no token for the login given on %s", a.Host),
				fmt.Sprintf("run `gh auth login --hostname %s` as that login, or give another --token source", a.Host))
		}
		if err != nil {
			return "", NewError(CodeNoToken,
				fmt.Sprintf("gh gave no token for %s on %s: %v", a.Token.Arg, a.Host, err),
				fmt.Sprintf("run `gh auth login --hostname %s` as %s, or change `token` of account %q in config.toml", a.Host, a.Token.Arg, a.Name))
		}
		return t, nil
	}
	return "", NewError(CodeConfigInvalid, fmt.Sprintf("account %q has no token source", a.Name),
		fmt.Sprintf("set `token = \"stored\"` (or env:<VAR>, gh:<login>) in account %q in config.toml", a.Name))
}

// fingerprint is what a looked-up identity depends on, hashed so that the
// token itself never reaches the cache.
func fingerprint(c Credential) string {
	sum := sha256.Sum256([]byte(c.Host + "\x00" + c.User + "\x00" + c.Token))
	return hex.EncodeToString(sum[:])
}

// WhoAmI answers whose the credential's identity is: the token's owner, or
// the account's `user` override looked up. The answer is cached, keyed by
// account name and a hash of the token, so a changed token is looked up again.
func (s *Service) WhoAmI(ctx context.Context, c Credential) (Identity, error) {
	fp := fingerprint(c)
	if id, ok := s.p.Cache.Get(c.Account, fp); ok {
		return id, nil
	}
	id, err := s.identity(ctx, c)
	if err != nil {
		return Identity{}, err
	}
	_ = s.p.Cache.Put(c.Account, fp, id) // a cache that cannot be written only costs a lookup
	return id, nil
}

func (s *Service) identity(ctx context.Context, c Credential) (Identity, error) {
	if c.User != "" {
		return s.p.Prober.Lookup(ctx, c, c.User)
	}
	p, err := s.p.Prober.Probe(ctx, c)
	return p.Identity, err
}

// Check probes the credential: whose it is, that it reads each owner (none
// for a Jira site) and whether it can write.
func (s *Service) Check(ctx context.Context, c Credential, owners []string) (Probe, error) {
	p, err := s.p.Prober.Probe(ctx, c)
	if err != nil {
		return Probe{}, err
	}
	if c.User != "" {
		if p.Identity, err = s.p.Prober.Lookup(ctx, c, c.User); err != nil {
			return Probe{}, err
		}
	}
	if c.Service != Jira {
		for _, o := range owners {
			if err := s.p.Prober.Owner(ctx, c, o); err != nil {
				return Probe{}, err
			}
		}
	}
	return p, nil
}

// List is every configured account, without its token.
func (s *Service) List() ([]AccountView, error) {
	accounts, err := s.accounts()
	if err != nil {
		return nil, err
	}
	out := make([]AccountView, len(accounts))
	for i, a := range accounts {
		out[i] = view(a)
	}
	return out, nil
}

// UnusedStored names the entries of credentials.toml that no account names.
func (s *Service) UnusedStored() ([]string, error) {
	accounts, err := s.p.Config.Accounts()
	if err != nil {
		return nil, err
	}
	names, err := s.p.Vault.Names()
	if err != nil {
		return nil, err
	}
	var unused []string
	for _, n := range names {
		if _, ok := domain.Find(accounts, n); !ok {
			unused = append(unused, n)
		}
	}
	sort.Strings(unused)
	return slices.Compact(unused), nil
}
