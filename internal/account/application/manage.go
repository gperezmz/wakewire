package application

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gperezmz/wakewire/internal/account/domain"
)

// What a command found and did. Under DryRun, the outcome is what it would do.
const (
	OutcomeAdded    = "added"          // a new account
	OutcomeAlready  = "already"        // the state asked for was already there
	OutcomeStored   = "token-stored"   // the account was there, its token was not
	OutcomeReplaced = "token-replaced" // the account was there, with another token
	OutcomeRenamed  = "renamed"
	OutcomeRemoved  = "removed"
	OutcomeAbsent   = "absent" // nothing to remove
)

// AddRequest is `account add`.
type AddRequest struct {
	Name        string
	Host        string
	Owners      []string
	Email       string
	User        string
	TokenSource string // "stored" when empty
	Token       string // the token itself, read by the caller, for TokenSource "stored"
	DryRun      bool
}

// AddResult is what `account add` did. Identity and CanWrite are nil when no
// check ran because the account was already as asked; CanWrite is also nil
// when the service does not say.
type AddResult struct {
	Outcome  string      `json:"outcome"`
	DryRun   bool        `json:"dryRun"`
	Account  AccountView `json:"account"`
	Identity *Identity   `json:"identity"`
	CanWrite *bool       `json:"canWrite"`
}

var flagFor = map[string]string{
	"name": "the <name> argument", "host": "--host", "owners": "--owner",
	"email": "--email", "token": "--token",
}

func invalid(err error) error {
	field := "name"
	if v, ok := errors.AsType[*domain.Violation](err); ok {
		field = v.Field
	}
	return NewError(CodeInvalidValue, err.Error(), fmt.Sprintf("correct %s; `wakewire account add --help` shows the form of each flag", flagFor[field]))
}

func (r AddRequest) account() (domain.Account, error) {
	src := r.TokenSource
	if src == "" {
		src = "stored"
	}
	ts, err := domain.ParseTokenSource(src)
	if err != nil {
		return domain.Account{}, invalid(err)
	}
	a := domain.Account{
		Name: strings.TrimSpace(r.Name), Host: strings.ToLower(strings.TrimSpace(r.Host)),
		Email: strings.TrimSpace(r.Email), User: strings.TrimSpace(r.User), Token: ts,
	}
	for _, o := range r.Owners {
		o = strings.TrimSpace(o)
		if o != "" && !containsFold(a.Owners, o) {
			a.Owners = append(a.Owners, o)
		}
	}
	if err := domain.ValidName(a.Name); err != nil {
		return domain.Account{}, invalid(err)
	}
	if err := a.Validate(); err != nil {
		return domain.Account{}, invalid(err)
	}
	return a, nil
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

// Add checks the token and only then writes the account to config.toml and,
// for a stored token, to credentials.toml. Adding what is already there
// changes nothing; adding an existing account with another stored token
// replaces the token.
func (s *Service) Add(ctx context.Context, r AddRequest) (AddResult, error) {
	a, err := r.account()
	if err != nil {
		return AddResult{}, err
	}
	accounts, err := s.accounts()
	if err != nil {
		return AddResult{}, err
	}
	existing, exists := domain.Find(accounts, a.Name)
	switch {
	case exists && !existing.Same(a):
		return AddResult{}, NewError(CodeAccountExists,
			fmt.Sprintf("an account named %q already exists and is not the one asked for (%s)", a.Name, describe(existing)),
			fmt.Sprintf("choose another name, or `wakewire account remove %s` first, or edit it in config.toml", a.Name))
	case !exists:
		if other, clash := domain.Conflict(accounts, a); clash {
			return AddResult{}, NewError(CodeAccountClash,
				fmt.Sprintf("account %q already covers %s", other.Name, a.Host+ownersText(a)),
				fmt.Sprintf("`wakewire account remove %s` first, or leave that owner out of this one", other.Name))
		}
	}
	res := AddResult{DryRun: r.DryRun, Account: view(a), Outcome: OutcomeAdded}

	// The token this account will read with, and what is already stored.
	token := strings.TrimSpace(r.Token)
	stored, hasStored := "", false
	switch a.Token.Kind {
	case domain.Stored:
		if token == "" {
			return AddResult{}, NewError(CodeInvalidValue, "no token was given for the stored token source",
				"pipe the token on stdin (`… | wakewire account add …`), or run it in a terminal to be asked; a token is never taken from a flag")
		}
		if stored, hasStored, err = s.p.Vault.Token(a.Name); err != nil {
			return AddResult{}, err
		}
		if exists && hasStored && stored == token {
			res.Outcome = OutcomeAlready
			return res, nil
		}
	default:
		if exists {
			res.Outcome = OutcomeAlready
			return res, nil
		}
		if token, err = s.resolve(a, false); err != nil {
			return AddResult{}, err
		}
	}

	probe, err := s.Check(ctx, Credential{
		Account: a.Name, Host: a.Host, Service: a.Service(), Token: token,
		TokenSource: a.Token.String(), User: a.User, Email: a.Email,
	}, a.Owners)
	if err != nil {
		return AddResult{}, err
	}
	id := probe.Identity
	res.Identity = &id
	if probe.CanWriteKnown {
		res.CanWrite = &probe.CanWrite
	}
	switch {
	case exists && hasStored:
		res.Outcome = OutcomeReplaced
	case exists:
		res.Outcome = OutcomeStored
	}
	if r.DryRun {
		return res, nil
	}
	if a.Token.Kind == domain.Stored {
		if err := s.p.Vault.Put(a.Name, token); err != nil {
			return AddResult{}, err
		}
	}
	if !exists {
		if err := s.p.Config.Add(a); err != nil {
			return AddResult{}, err
		}
	}
	return res, nil
}

func describe(a domain.Account) string {
	return a.Host + ownersText(a) + ", token " + a.Token.String()
}

func ownersText(a domain.Account) string {
	if len(a.Owners) == 0 {
		return ""
	}
	return " for " + strings.Join(a.Owners, ", ")
}

// RenameResult is what `account rename` did.
type RenameResult struct {
	Outcome    string `json:"outcome"`
	DryRun     bool   `json:"dryRun"`
	From       string `json:"from"`
	To         string `json:"to"`
	TokenMoved bool   `json:"tokenMoved"`
}

// Rename changes an account's name in config.toml and its entry in
// credentials.toml together. Renaming to the name it already has, or one
// whose old name is gone, is already done.
func (s *Service) Rename(oldName, newName string, dryRun bool) (RenameResult, error) {
	if err := domain.ValidName(newName); err != nil {
		return RenameResult{}, invalid(err)
	}
	accounts, err := s.accounts()
	if err != nil {
		return RenameResult{}, err
	}
	res := RenameResult{Outcome: OutcomeRenamed, DryRun: dryRun, From: oldName, To: newName}
	_, hasOld := domain.Find(accounts, oldName)
	_, hasNew := domain.Find(accounts, newName)
	switch {
	case oldName == newName && hasOld, !hasOld && hasNew:
		res.Outcome = OutcomeAlready
		return res, nil
	case !hasOld:
		return RenameResult{}, NewError(CodeNoSuchAccount, fmt.Sprintf("no account is named %q", oldName),
			"`wakewire account list` shows the accounts")
	case hasNew:
		return RenameResult{}, NewError(CodeAccountExists, fmt.Sprintf("an account named %q already exists", newName),
			fmt.Sprintf("choose another name, or `wakewire account remove %s` first", newName))
	}
	token, hasToken, err := s.p.Vault.Token(oldName)
	if err != nil {
		return RenameResult{}, err
	}
	res.TokenMoved = hasToken
	if dryRun {
		return res, nil
	}
	// The new entry is written before the config changes and the old one
	// dropped after, so a failure in between leaves a state a repeat finishes.
	if hasToken {
		if err := s.p.Vault.Put(newName, token); err != nil {
			return RenameResult{}, err
		}
	}
	if err := s.p.Config.Rename(oldName, newName); err != nil {
		return RenameResult{}, err
	}
	if hasToken {
		if err := s.p.Vault.Delete(oldName); err != nil {
			return RenameResult{}, err
		}
	}
	_ = s.p.Cache.Forget(oldName)
	return res, nil
}

// RemoveResult is what `account remove` did.
type RemoveResult struct {
	Outcome        string `json:"outcome"`
	DryRun         bool   `json:"dryRun"`
	Name           string `json:"name"`
	AccountRemoved bool   `json:"accountRemoved"`
	TokenRemoved   bool   `json:"tokenRemoved"`
}

// Remove deletes the account and its stored token. A stored token no account
// names (what `doctor` warns about) is deleted by name alone. Removing what
// is gone is already done.
func (s *Service) Remove(name string, dryRun bool) (RemoveResult, error) {
	accounts, err := s.p.Config.Accounts()
	if err != nil {
		return RemoveResult{}, err
	}
	_, hasAccount := domain.Find(accounts, name)
	_, hasToken, err := s.p.Vault.Token(name)
	if err != nil {
		return RemoveResult{}, err
	}
	res := RemoveResult{Outcome: OutcomeRemoved, DryRun: dryRun, Name: name, AccountRemoved: hasAccount, TokenRemoved: hasToken}
	if !hasAccount && !hasToken {
		res.Outcome = OutcomeAbsent
		return res, nil
	}
	if dryRun {
		return res, nil
	}
	if hasAccount {
		if err := s.p.Config.Remove(name); err != nil {
			return RemoveResult{}, err
		}
	}
	if hasToken {
		if err := s.p.Vault.Delete(name); err != nil {
			return RemoveResult{}, err
		}
	}
	_ = s.p.Cache.Forget(name)
	return res, nil
}
