// Package configstore is the account context's view of config.toml: its
// accounts, read and edited through the shared config package.
package configstore

import (
	"github.com/gperezmz/wakewire/internal/account/domain"
	"github.com/gperezmz/wakewire/internal/adapters/config"
	"github.com/gperezmz/wakewire/internal/adapters/home"
)

// Store reads and edits the accounts of one config.toml.
type Store struct {
	paths home.Paths
}

// New returns the store of the config.toml that paths names.
func New(paths home.Paths) *Store { return &Store{paths: paths} }

// Accounts lists the accounts as written. A token source that is not one of
// the three comes back as the zero source, which the account's Validate names.
func (s *Store) Accounts() ([]domain.Account, error) {
	f, err := config.Load(s.paths.Config)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Account, len(f.Accounts))
	for i, a := range f.Accounts {
		ts, _ := domain.ParseTokenSource(a.Token)
		out[i] = domain.Account{Name: a.Name, Host: a.Host, Owners: a.Owners, Email: a.Email, User: a.User, Token: ts}
	}
	return out, nil
}

// Add appends the account to config.toml, creating the folder and file.
func (s *Store) Add(a domain.Account) error {
	if err := s.paths.Ensure(); err != nil {
		return config.NewError("cannot create "+s.paths.Home+": "+err.Error(), "check that its parent is writable")
	}
	return config.AppendAccount(s.paths.Config, config.Account{
		Name: a.Name, Host: a.Host, Owners: a.Owners, Email: a.Email, Token: a.Token.String(), User: a.User,
	})
}

// Rename renames an account in place.
func (s *Store) Rename(oldName, newName string) error {
	return config.RenameAccount(s.paths.Config, oldName, newName)
}

// Remove deletes an account's block.
func (s *Store) Remove(name string) error {
	return config.RemoveAccount(s.paths.Config, name)
}
