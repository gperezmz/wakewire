package cli

import (
	"fmt"
	"io"
	"strings"
)

// writeHelp prints a command's help, in plain text: the usage line, what it
// does, its flags with their defaults, the global flags in one line, then
// its examples and exit codes. It lists a group's subcommands, and the root's
// commands with the ones only an adapter calls apart.
func writeHelp(w io.Writer, c *command) {
	var b strings.Builder
	usage := c.usageLine()
	switch {
	case len(c.subs) > 0 && c.parent == nil:
		usage += " [<command>]"
	case len(c.subs) > 0:
		usage += " <command>"
	}
	if len(c.ownFlags()) > 0 {
		usage += " [flags]"
	}
	b.WriteString(usage + "\n" + c.usage + "\n")
	if c.desc != "" {
		b.WriteString("\n" + c.desc + "\n")
	}
	helpCommands(&b, c)
	if flags := c.ownFlags(); len(flags) > 0 {
		b.WriteString("\nFlags:\n")
		rows := make([][2]string, len(flags))
		for i, f := range flags {
			rows[i] = [2]string{flagLabel(f), flagText(f)}
		}
		writeRows(&b, rows)
	}
	b.WriteString("\nGlobal:")
	for i, f := range globals {
		if i > 0 {
			b.WriteString(";")
		}
		b.WriteString(" " + flagLabel(f) + "  " + f.usage)
	}
	b.WriteString("\n")
	if len(c.examples) > 0 {
		b.WriteString("\nExamples:\n")
		for _, e := range c.examples {
			b.WriteString("  " + e + "\n")
		}
	}
	if c.exits != "" {
		b.WriteString("\nExit codes: " + c.exits + "\n")
	}
	if c.related != "" {
		b.WriteString("Related: " + c.related + "\n")
	}
	_, _ = io.WriteString(w, b.String())
}

// helpCommands lists a command's subcommands, those apart under their own
// heading.
func helpCommands(b *strings.Builder, c *command) {
	var plain [][2]string
	apart := map[string][][2]string{}
	var headings []string
	for _, s := range c.subs {
		row := [2]string{s.name, s.usage}
		if s.apart == "" {
			plain = append(plain, row)
			continue
		}
		if _, ok := apart[s.apart]; !ok {
			headings = append(headings, s.apart)
		}
		apart[s.apart] = append(apart[s.apart], row)
	}
	if len(plain) > 0 {
		b.WriteString("\nCommands:\n")
		writeRows(b, plain)
	}
	for _, h := range headings {
		b.WriteString("\n" + h + ":\n")
		writeRows(b, apart[h])
	}
}

// flagLabel is a flag as it is typed: --since <duration>, or --json.
func flagLabel(f flagSpec) string {
	label := "--" + f.name
	if f.short != "" {
		label += ", -" + f.short
	}
	switch {
	case f.value != "":
		label += " " + f.value
	case f.kind == stringFlag, f.kind == listFlag:
		label += " <string>"
	case f.kind == intFlag:
		label += " <int>"
	}
	return label
}

// flagText is what a flag does, with whether it is required and its default.
func flagText(f flagSpec) string {
	text := f.usage
	if f.required {
		text += " (required)"
	}
	if f.def != "" {
		text += fmt.Sprintf(" (default %s)", f.def)
	}
	return text
}

// writeRows prints two columns, the first padded to its longest.
func writeRows(b *strings.Builder, rows [][2]string) {
	width := 0
	for _, r := range rows {
		width = max(width, len(r[0]))
	}
	for _, r := range rows {
		fmt.Fprintf(b, "  %-*s  %s\n", width, r[0], r[1])
	}
}
