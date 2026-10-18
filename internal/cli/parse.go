package cli

import (
	"fmt"
	"slices"
	"strings"
)

// parse reads a command line against the table, in one pass. It never
// stops at a refusal: it keeps the first, and keeps reading, so the same
// pass says whether --json or --help was asked for wherever they stood.
// What follows a "--" is arguments, whatever it reads.
//
// A flag is --name or -name, with its value as --name=v or, for a flag that
// takes one, as the next word; a word that starts with - takes the place of
// a value only as -v=... would, unless it is a number. A word that is a
// number, such as -1, is never a flag: it is an argument, or a value.
func parse(root *command, argv []string) *call {
	c := &call{cmd: root, given: map[string][]string{}}
	var flagErr, unknown error
	var repeated string
	lost, endFlags := false, false // lost: a word named no command
	for i := 0; i < len(argv); i++ {
		w := argv[i]
		if !endFlags && flagLike(w) {
			f, err := c.readFlag(argv, &i)
			if err != nil && flagErr == nil {
				flagErr = err
			}
			if f != "" && repeated == "" && len(c.given[f]) > 1 && !c.isList(f) {
				repeated = f
			}
			continue
		}
		c.typed = append(c.typed, typedWord{words: []string{w}})
		switch {
		case !endFlags && w == "--":
			endFlags = true
		case len(c.cmd.subs) == 0 || lost:
			c.args = append(c.args, w)
		case c.cmd.sub(w) != nil:
			c.cmd = c.cmd.sub(w)
		default:
			lost, unknown = true, unknownCommand(c.cmd, w)
		}
	}
	for _, err := range []error{flagErr, unknown, c.missingFlags(), repeatedFlag(c.cmd, repeated), c.argCount()} {
		if err != nil {
			c.err = err
			break
		}
	}
	return c
}

// flagLike says whether a word is read as a flag: it starts with - and is
// not a lone -, the end of the flags or a number.
func flagLike(w string) bool {
	return strings.HasPrefix(w, "-") && w != "-" && w != "--" && !isNumber(w)
}

// isNumber says whether a word is a minus sign and digits, as -1.
func isNumber(w string) bool {
	digits, ok := strings.CutPrefix(w, "-")
	return ok && digits != "" && strings.Trim(digits, "0123456789") == ""
}

// readFlag reads the flag at argv[*i], and the next word if the flag takes
// it as its value, then moves *i past what it read. It answers the name of
// the flag it set, if any, and the refusal of the word, if any.
func (c *call) readFlag(argv []string, i *int) (string, error) {
	w := argv[*i]
	typed := typedWord{words: []string{w}}
	defer func() { c.typed = append(c.typed, typed) }()
	name, value, hasValue := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(w, "-"), "-"), "=")
	f, ok := c.cmd.lookup(name)
	if !ok {
		return "", unknownFlag(c.cmd, w, name)
	}
	typed.flag = f.name
	switch {
	case !hasValue && f.kind == boolFlag:
		value = "true"
	case !hasValue:
		if *i+1 >= len(argv) || !isValue(argv[*i+1]) {
			return "", missingValue(f)
		}
		*i++
		value = argv[*i]
		typed.words = append(typed.words, value)
	}
	if !f.accepts(value) {
		return "", invalidValue(f, value)
	}
	c.given[f.name] = append(c.given[f.name], value)
	return f.name, nil
}

// isValue says whether a word may follow a flag as its value: anything but
// another flag, so a value that starts with - is given as --name=value.
func isValue(w string) bool { return !flagLike(w) && w != "--" }

func (c *call) isList(name string) bool {
	f, _ := c.cmd.lookup(name)
	return f.kind == listFlag
}

// missingFlags refuses a line that lacks flags its command requires.
func (c *call) missingFlags() error {
	var names []string
	for _, f := range c.cmd.flags {
		if f.required && len(c.given[f.name]) == 0 {
			names = append(names, "--"+f.name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	list := strings.Join(names, ", ")
	return badUsage("missing required flag "+list, "usage: "+c.cmd.fullName()+" "+list+" <value>")
}

// argCount refuses too few or too many arguments, as the command's table
// names them: those not optional at least, all of them at most.
func (c *call) argCount() error {
	least, most, n := 0, len(c.cmd.args), len(c.args)
	for _, a := range c.cmd.args {
		if !a.optional {
			least++
		}
	}
	switch {
	case n > most:
		return badUsage(fmt.Sprintf("%s takes at most %d argument(s), got %d: %s", c.cmd.fullName(), most, n,
			strings.Join(c.args, " ")), c.cmd.usageLine())
	case n < least:
		return badUsage(fmt.Sprintf("%s takes at least %d argument(s), got %d", c.cmd.fullName(), least, n), c.cmd.usageLine())
	}
	return nil
}

// The refusals of a command line, each built here once. A usage refusal is a
// line wakewire cannot read; an invalid value is a word it read and cannot use.

// unknownFlag refuses a flag the command does not declare, as it was typed
// and without its value, and lists the ones it does, so the next try needs
// no --help. A flag the table says was removed is refused as that says.
func unknownFlag(cmd *command, word, name string) error {
	if refusal, ok := cmd.removed[name]; ok {
		return refusal
	}
	hint := validFlags(cmd)
	if name == "v" || name == "V" || name == "version" {
		hint = "--version stands alone, as `wakewire --version`; " + hint
	}
	typed, _, _ := strings.Cut(word, "=")
	return badUsage(fmt.Sprintf("unknown flag %s for `%s`", typed, cmd.fullName()), hint)
}

// validFlags lists the flags a command declares.
func validFlags(cmd *command) string {
	var names []string
	for _, f := range cmd.ownFlags() {
		names = append(names, "--"+f.name)
	}
	if len(names) == 0 {
		return fmt.Sprintf("`%s` takes no flags but --help and --json", cmd.fullName())
	}
	return fmt.Sprintf("valid flags for `%s`: %s (--help and --json always allowed)", cmd.fullName(), strings.Join(names, ", "))
}

// flagHint names what a flag takes, from its own usage line.
func flagHint(f flagSpec) string { return fmt.Sprintf("--%s: %s", f.name, f.usage) }

// missingValue refuses a flag that takes a value and was given none.
func missingValue(f flagSpec) error {
	return badUsage(fmt.Sprintf("--%s needs a value", f.name),
		flagHint(f)+fmt.Sprintf(" (give a value that starts with - as --%s=<value>)", f.name))
}

// invalidValue refuses a value the flag's kind does not read.
func invalidValue(f flagSpec, value string) error {
	return usageError(fmt.Sprintf("--%s %q is not a valid value", f.name, value), flagHint(f))
}

// repeatedFlag refuses a flag that takes one value, given twice; the hint
// names the flags that may repeat, which the table says by their kind.
func repeatedFlag(cmd *command, name string) error {
	if name == "" {
		return nil
	}
	var lists []string
	root := cmd
	for root.parent != nil {
		root = root.parent
	}
	var walk func(*command)
	walk = func(c *command) {
		for _, f := range c.flags {
			if f.kind == listFlag && !slices.Contains(lists, "--"+f.name) {
				lists = append(lists, "--"+f.name)
			}
		}
		for _, s := range c.subs {
			walk(s)
		}
	}
	walk(root)
	return badUsage(fmt.Sprintf("--%s is given more than once", name),
		fmt.Sprintf("give --%s once; only %s repeat", name, strings.Join(lists, " and ")))
}

// unknownCommand refuses a command name and lists the commands there are.
func unknownCommand(cmd *command, word string) error {
	where := cmd.fullName()
	if word == "help" {
		return badUsage(fmt.Sprintf("`%s` has no help command", where),
			fmt.Sprintf("Run `%s --help`, or `%s <command> --help` for one command", where, where))
	}
	var names []string
	for _, s := range cmd.subs {
		names = append(names, s.name)
	}
	return badUsage(fmt.Sprintf("unknown command %q for `%s`", word, where),
		fmt.Sprintf("commands of `%s`: %s", where, strings.Join(names, ", ")))
}
