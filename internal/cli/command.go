package cli

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// kind is what a flag takes.
type kind int

const (
	boolFlag   kind = iota // --f, or --f=true or --f=false
	stringFlag             // --f v, or --f=v
	intFlag                // a whole number
	listFlag               // a string, and the only kind that may be given again
)

// flagSpec is one option of a command: the one place its name, its kind, its
// default and its usage are written, for the parser and for --help alike.
type flagSpec struct {
	name     string
	short    string // a one-letter spelling, as -h
	kind     kind
	def      string // what the flag holds when it is not given, and is shown as
	usage    string
	value    string // what --help shows the value as, such as <duration>; the kind when empty
	required bool
	hidden   bool // accepted, but not listed in --help or in the valid flags
}

// accepts says whether a value reads as the flag's kind.
func (f flagSpec) accepts(value string) bool {
	switch f.kind {
	case boolFlag:
		return value == "true" || value == "false"
	case intFlag:
		_, err := strconv.Atoi(value)
		return err == nil
	}
	return true
}

// arg is one positional argument of a command.
type arg struct {
	name     string
	optional bool
}

// command is one entry of the table every part of the CLI reads: the parser
// checks a command line against it, --help prints it and the action runs it.
// A group, such as account, has subcommands, and its own action answers when
// no subcommand is named.
type command struct {
	name     string
	usage    string // one line: what it does
	desc     string
	examples []string
	exits    string // what each exit code means
	related  string
	args     []arg
	flags    []flagSpec
	// removed are flags an older caller still passes, each with the refusal
	// that says what to do instead.
	removed map[string]*Failure
	action  func(ctx context.Context, cmd *call) error
	subs    []*command
	// apart is the heading a parent's --help lists this command under, when
	// it is not one an agent runs.
	apart  string
	parent *command
}

// globals are the only flags every command takes.
var globals = []flagSpec{
	{name: "json", kind: boolFlag, usage: "print exactly one JSON object on stdout"},
	{name: "help", short: "h", kind: boolFlag, usage: "show help"},
}

// link sets every command's parent, so a name reads from the root down.
func (c *command) link() *command {
	for _, s := range c.subs {
		s.parent = c
		s.link()
	}
	return c
}

// fullName is the command as typed, from the binary's name: wakewire account add.
func (c *command) fullName() string {
	if c.parent == nil {
		return c.name
	}
	return c.parent.fullName() + " " + c.name
}

// usageLine is the command with its arguments: usage: wakewire changes <batch> [<n>].
func (c *command) usageLine() string {
	words := []string{c.fullName()}
	for _, a := range c.args {
		if a.optional {
			words = append(words, "[<"+a.name+">]")
		} else {
			words = append(words, "<"+a.name+">")
		}
	}
	return "usage: " + strings.Join(words, " ")
}

// sub finds a subcommand by name.
func (c *command) sub(name string) *command {
	for _, s := range c.subs {
		if s.name == name {
			return s
		}
	}
	return nil
}

// lookup finds the flag a word names among the command's own and the globals.
func (c *command) lookup(name string) (flagSpec, bool) {
	for _, f := range c.flags {
		if f.name == name {
			return f, true
		}
	}
	for _, f := range globals {
		if f.name == name || (f.short != "" && f.short == name) {
			return f, true
		}
	}
	return flagSpec{}, false
}

// ownFlags are the flags the command lists: its own, less the hidden ones.
func (c *command) ownFlags() []flagSpec {
	var out []flagSpec
	for _, f := range c.flags {
		if !f.hidden {
			out = append(out, f)
		}
	}
	return out
}

// call is one command line read against the table: the command it reached,
// its arguments, every flag value as typed and the words themselves.
type call struct {
	cmd   *command
	args  []string
	given map[string][]string // each flag's values, in the order typed
	typed []typedWord
	// err is the first refusal of the line, which no action then runs.
	err error
}

// typedWord is one word of the line, or two where a flag took the next word
// as its value; flag names the flag it set, or is empty for any other word.
type typedWord struct {
	words []string
	flag  string
}

func (c *call) declared(name string) flagSpec {
	if f, ok := c.cmd.lookup(name); ok {
		return f
	}
	panic(fmt.Sprintf("wakewire: %s has no --%s flag", c.cmd.fullName(), name))
}

// String is the last value given for a flag, or its default.
func (c *call) String(name string) string {
	f := c.declared(name)
	if v := c.given[f.name]; len(v) > 0 {
		return v[len(v)-1]
	}
	return f.def
}

// Strings are every value given for a list flag, in order.
func (c *call) Strings(name string) []string { return c.given[c.declared(name).name] }

// Bool says whether a flag was given as true.
func (c *call) Bool(name string) bool { return c.String(name) == "true" }

// Int is a flag's number; the parser has refused any other value.
func (c *call) Int(name string) int {
	n, _ := strconv.Atoi(c.String(name))
	return n
}

// Arg is the i-th argument, or "" where there is none.
func (c *call) Arg(i int) string {
	if i < len(c.args) {
		return c.args[i]
	}
	return ""
}

// fullName is the command that was called, as typed from the binary's name.
func (c *call) fullName() string { return c.cmd.fullName() }

// rerun is the command line as typed, as a command a hint can name, less
// the flags in drop and the values they took.
func (c *call) rerun(drop ...string) string {
	parts := []string{"wakewire"}
	for _, t := range c.typed {
		if t.flag != "" && slices.Contains(drop, t.flag) {
			continue
		}
		for _, w := range t.words {
			parts = append(parts, shellWord(w))
		}
	}
	return strings.Join(parts, " ")
}
