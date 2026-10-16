// Package config reads config.toml, the file people write and share, and
// edits its accounts in place; see docs/reference/configuration.md. It holds
// no token: those live in credentials.toml.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// MinInterval is the shortest poll interval, as configuration.md fixes it.
const MinInterval = 30 * time.Second

// Account is one [[accounts]] entry as written.
type Account struct {
	Name   string
	Host   string
	Owners []string
	Email  string
	Token  string
	User   string
}

// SourceTemplate says where in a repository to read the tracker and labels.
type SourceTemplate struct {
	Files   []string
	Heading string
	Tracker string
	Table   struct {
		Key   string
		Value string
	}
	Labels struct {
		Spec       string
		Ready      string
		InProgress string `toml:"in_progress"`
		NeedsInput string `toml:"needs_input"`
	}
}

// File is config.toml with every default filled in.
type File struct {
	Poll struct {
		Interval string
	}
	Echo struct {
		Marker string
	}
	Accounts []Account
	Source   SourceTemplate
}

// Defaults is what a missing config.toml means.
func Defaults() File {
	var f File
	f.Poll.Interval = "2m"
	f.Echo.Marker = "🤖 unattendish agent"
	f.Source.Files = []string{"AGENTS.md", "CLAUDE.md"}
	f.Source.Heading = "Forge and tracker"
	f.Source.Tracker = "Tracker:"
	f.Source.Table.Key = "Meaning"
	f.Source.Table.Value = "Label"
	f.Source.Labels.Spec = "spec"
	f.Source.Labels.Ready = "ready"
	f.Source.Labels.InProgress = "in progress"
	f.Source.Labels.NeedsInput = "needs input"
	return f
}

// Error is a configuration that cannot be used. Its code is config_invalid.
type Error struct{ message, hint string }

func (e *Error) Error() string { return e.message }
func (e *Error) Code() string  { return "config_invalid" }
func (e *Error) Hint() string  { return e.hint }

// NewError builds a config_invalid error.
func NewError(message, hint string) *Error { return &Error{message: message, hint: hint} }

// Load reads path over the defaults: a missing file is all defaults, and a
// key the file sets replaces only itself. A file that does not parse, repeats
// an account name or has two accounts claiming one host and owner is
// config_invalid.
func Load(path string) (File, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Defaults(), nil
	}
	if err != nil {
		return File{}, NewError(fmt.Sprintf("cannot read %s: %v", path, err), "check the file's permissions")
	}
	return parse(path, string(data))
}

func parse(path, text string) (File, error) {
	f := Defaults()
	if _, err := toml.Decode(text, &f); err != nil {
		var pe toml.ParseError
		if errors.As(err, &pe) {
			return File{}, NewError(fmt.Sprintf("%s does not parse: %s (line %d)", path, pe.Message, pe.Position.Line),
				fmt.Sprintf("fix line %d of %s, or move the file aside to start from the defaults", pe.Position.Line, path))
		}
		return File{}, NewError(fmt.Sprintf("%s does not parse: %v", path, err), "fix the file, or move it aside to start from the defaults")
	}
	if err := checkInterval(path, f.Poll.Interval); err != nil {
		return File{}, err
	}
	if err := checkAccounts(path, text, f.Accounts); err != nil {
		return File{}, err
	}
	return f, nil
}

func checkInterval(path, s string) error {
	d, err := time.ParseDuration(s)
	if err != nil || d < MinInterval {
		return NewError(fmt.Sprintf("[poll] interval %q in %s is not a duration of at least %s", s, path, MinInterval),
			fmt.Sprintf("set `interval = \"2m\"` (or another duration from 30s) under [poll] in %s", path))
	}
	return nil
}

func checkAccounts(path, text string, accounts []Account) error {
	blocks := scan(strings.Split(text, "\n"))
	line := func(i int) string {
		if i < len(blocks) {
			return fmt.Sprintf(" (line %d)", blocks[i].header+1)
		}
		return ""
	}
	names := map[string]int{}
	claims := map[string]int{}
	for i, a := range accounts {
		if a.Name == "" {
			return NewError(fmt.Sprintf("an account in %s has no name%s", path, line(i)),
				fmt.Sprintf("add `name = \"…\"` to that [[accounts]] block in %s", path))
		}
		if a.Host == "" {
			return NewError(fmt.Sprintf("account %q has no host%s", a.Name, line(i)),
				fmt.Sprintf("add `host = \"…\"` to account %q in %s", a.Name, path))
		}
		if j, dup := names[a.Name]; dup {
			return NewError(fmt.Sprintf("two accounts are named %q%s%s", a.Name, line(j), line(i)),
				fmt.Sprintf("rename or remove one of them: `wakewire account rename %s <new name>`, or edit %s", a.Name, path))
		}
		names[a.Name] = i
		owners := a.Owners
		if len(owners) == 0 {
			owners = []string{""} // a Jira site has no owners: the host is the claim
		}
		for _, o := range owners {
			key := strings.ToLower(a.Host) + "/" + strings.ToLower(o)
			if j, dup := claims[key]; dup {
				fix := fmt.Sprintf("remove %q from one account's owners in %s, or `wakewire account remove <name>` one of them", o, path)
				if o == "" {
					fix = fmt.Sprintf("`wakewire account remove <name>` one of them, or edit %s", path)
				}
				return NewError(fmt.Sprintf("accounts %q and %q both cover %s%s%s", accounts[j].Name, a.Name, strings.TrimSuffix(key, "/"), line(j), line(i)), fix)
			}
			claims[key] = i
		}
	}
	return nil
}
