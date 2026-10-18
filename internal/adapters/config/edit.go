package config

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/gperezmz/wakewire/v2/internal/adapters/atomicfile"
)

// The edits below change config.toml as text, so the comments and layout
// people gave it survive: an added account is a new block at the end, a
// renamed one has its name line rewritten, a removed one loses its block.

var (
	headerRE   = regexp.MustCompile(`^\s*\[\[\s*accounts\s*\]\]\s*(#.*)?$`)
	tableRE    = regexp.MustCompile(`^\s*\[\[?\s*[A-Za-z0-9_."' -]+\s*\]\]?\s*(#.*)?$`)
	nameLineRE = regexp.MustCompile(`^(\s*name\s*=\s*)("(?:[^"\\]|\\.)*"|'[^']*')(.*)$`)
)

// block is one [[accounts]] table: the line of its header and of its last
// line that holds a key, a value or a continuation (not a blank or comment).
type block struct {
	header, last int
	name         string
}

func scan(lines []string) []block {
	var blocks []block
	for i := 0; i < len(lines); i++ {
		if !headerRE.MatchString(lines[i]) {
			continue
		}
		b := block{header: i, last: i}
		for j := i + 1; j < len(lines) && !tableRE.MatchString(lines[j]); j++ {
			t := strings.TrimSpace(lines[j])
			if t != "" && !strings.HasPrefix(t, "#") {
				b.last = j
			}
		}
		var one struct {
			Accounts []struct {
				Name string `toml:"name"`
			} `toml:"accounts"`
		}
		if _, err := toml.Decode(strings.Join(lines[b.header:b.last+1], "\n"), &one); err == nil && len(one.Accounts) == 1 {
			b.name = one.Accounts[0].Name
		}
		blocks = append(blocks, b)
		i = b.last
	}
	return blocks
}

// AppendAccount adds an [[accounts]] block at the end of the file, creating the
// file when it is missing.
func AppendAccount(path string, a Account) error {
	text, mode, err := read(path)
	if err != nil {
		return err
	}
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if text != "" && !strings.HasSuffix(text, "\n\n") {
		text += "\n"
	}
	return write(path, text+render(a), mode)
}

// RenameAccount rewrites the name line of the account called old.
func RenameAccount(path, old, name string) error {
	text, mode, err := read(path)
	if err != nil {
		return err
	}
	lines := strings.Split(text, "\n")
	b, err := find(path, lines, old)
	if err != nil {
		return err
	}
	for i := b.header + 1; i <= b.last; i++ {
		if m := nameLineRE.FindStringSubmatch(lines[i]); m != nil {
			lines[i] = m[1] + quote(name) + m[3]
			return write(path, strings.Join(lines, "\n"), mode)
		}
	}
	return unreachableBlock(path, old, b)
}

// RemoveAccount deletes the block of the account called name, with the
// comment lines directly above it and one blank line beside it.
func RemoveAccount(path, name string) error {
	text, mode, err := read(path)
	if err != nil {
		return err
	}
	lines := strings.Split(text, "\n")
	b, err := find(path, lines, name)
	if err != nil {
		return err
	}
	start, end := b.header, b.last // inclusive
	for start > 0 && strings.HasPrefix(strings.TrimSpace(lines[start-1]), "#") {
		start--
	}
	switch {
	case end+1 < len(lines) && strings.TrimSpace(lines[end+1]) == "":
		end++
	case start > 0 && strings.TrimSpace(lines[start-1]) == "":
		start--
	}
	rest := append(append([]string{}, lines[:start]...), lines[end+1:]...)
	out := strings.Join(rest, "\n")
	if strings.TrimSpace(out) == "" {
		out = ""
	}
	return write(path, out, mode)
}

func find(path string, lines []string, name string) (block, error) {
	var found []block
	for _, b := range scan(lines) {
		if b.name == name {
			found = append(found, b)
		}
	}
	if len(found) != 1 {
		return block{}, NewError(fmt.Sprintf("%s has no single [[accounts]] block named %q that can be edited in place", path, name),
			fmt.Sprintf("edit %s by hand", path))
	}
	return found[0], nil
}

func unreachableBlock(path, name string, b block) error {
	return NewError(fmt.Sprintf("account %q (line %d of %s) writes its name in a form that cannot be edited in place", name, b.header+1, path),
		fmt.Sprintf("write it as `name = \"…\"` on its own line in %s, or edit the file by hand", path))
}

func read(path string) (string, os.FileMode, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", 0o644, nil
	}
	if err != nil {
		return "", 0, NewError(fmt.Sprintf("cannot read %s: %v", path, err), "check the file's permissions")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", 0, NewError(fmt.Sprintf("cannot read %s: %v", path, err), "check the file's permissions")
	}
	if _, err := parse(path, string(data)); err != nil {
		return "", 0, err
	}
	return string(data), info.Mode().Perm(), nil
}

// write checks that what it is about to put on disk still parses, so an
// edit can never leave a configuration that is broken.
func write(path, text string, mode os.FileMode) error {
	if _, err := parse(path, text); err != nil {
		return err
	}
	if err := atomicfile.Write(path, []byte(text), mode); err != nil {
		return NewError(fmt.Sprintf("cannot write %s: %v", path, err), "check that the folder exists and is writable")
	}
	return nil
}

func render(a Account) string {
	var b strings.Builder
	b.WriteString("[[accounts]]\n")
	field := func(key, value string) { fmt.Fprintf(&b, "%-6s = %s\n", key, value) }
	field("name", quote(a.Name))
	field("host", quote(a.Host))
	if len(a.Owners) > 0 {
		qs := make([]string, len(a.Owners))
		for i, o := range a.Owners {
			qs[i] = quote(o)
		}
		field("owners", "["+strings.Join(qs, ", ")+"]")
	}
	if a.Email != "" {
		field("email", quote(a.Email))
	}
	field("token", quote(a.Token))
	if a.User != "" {
		field("user", quote(a.User))
	}
	return b.String()
}

// quote writes s as a TOML basic string.
func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\r':
			b.WriteString(`\r`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
