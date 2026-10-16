// Package credfile keeps the stored tokens in credentials.toml, by account
// name, mode 600, written whole and atomically and only by `wakewire account`.
package credfile

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/BurntSushi/toml"

	"github.com/gperezmz/wakewire/internal/account/application"
	"github.com/gperezmz/wakewire/internal/adapters/atomicfile"
	"github.com/gperezmz/wakewire/internal/adapters/home"
)

const header = "# Written by `wakewire account`; edit it with that command, and never share or commit it.\n"

type entry struct {
	Token string `toml:"token"`
}

// Vault is the credentials.toml of one home folder.
type Vault struct {
	paths home.Paths
}

// New returns the vault that paths names.
func New(paths home.Paths) *Vault { return &Vault{paths: paths} }

func (v *Vault) read() (map[string]entry, error) {
	data, err := os.ReadFile(v.paths.Credentials)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]entry{}, nil
	}
	if err != nil {
		return nil, application.NewError(application.CodeConfigInvalid, "cannot read "+v.paths.Credentials+": "+err.Error(), "check the file's permissions")
	}
	entries := map[string]entry{}
	if _, err := toml.Decode(string(data), &entries); err != nil {
		// Only the line is named: the parser's message may quote a token.
		line := 0
		if pe, ok := errors.AsType[toml.ParseError](err); ok {
			line = pe.Position.Line
		}
		return nil, application.NewError(application.CodeConfigInvalid,
			fmt.Sprintf("%s does not parse (line %d)", v.paths.Credentials, line),
			fmt.Sprintf("fix line %d, or delete the file and store each token again with `wakewire account add <name> … --token stored`", line))
	}
	return entries, nil
}

func (v *Vault) write(entries map[string]entry) error {
	var buf bytes.Buffer
	buf.WriteString(header)
	if err := toml.NewEncoder(&buf).Encode(entries); err != nil {
		return application.NewError(application.CodeConfigInvalid, "cannot encode the stored tokens", "this is a bug in wakewire: report it")
	}
	if err := v.paths.Ensure(); err != nil {
		return application.NewError(application.CodeConfigInvalid, "cannot create "+v.paths.Home+": "+err.Error(), "check that its parent is writable")
	}
	if err := atomicfile.Write(v.paths.Credentials, buf.Bytes(), 0o600); err != nil {
		return application.NewError(application.CodeConfigInvalid, "cannot write "+v.paths.Credentials+": "+err.Error(), "check that the folder is writable")
	}
	return nil
}

// Token returns the token stored under name.
func (v *Vault) Token(name string) (string, bool, error) {
	entries, err := v.read()
	if err != nil {
		return "", false, err
	}
	e, ok := entries[name]
	return e.Token, ok && e.Token != "", nil
}

// Names lists every account name with an entry.
func (v *Vault) Names() ([]string, error) {
	entries, err := v.read()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for n := range entries {
		names = append(names, n)
	}
	sort.Strings(names)
	return names, nil
}

// Put stores token under name, replacing any earlier one.
func (v *Vault) Put(name, token string) error {
	entries, err := v.read()
	if err != nil {
		return err
	}
	entries[name] = entry{Token: token}
	return v.write(entries)
}

// Delete drops the entry under name.
func (v *Vault) Delete(name string) error {
	entries, err := v.read()
	if err != nil {
		return err
	}
	if _, ok := entries[name]; !ok {
		return nil
	}
	delete(entries, name)
	return v.write(entries)
}
