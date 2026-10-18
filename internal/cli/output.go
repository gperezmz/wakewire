package cli

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/toon-format/toon-go"
)

// reply is one command's default answer: an ordered object printed as TOON,
// with the next steps that follow from it last, under help.
type reply struct {
	fields []toon.Field
	help   []string
}

func newReply() *reply { return &reply{} }

// add appends a field; a nil value is left out, so optional facts cost nothing.
func (r *reply) add(key string, value any) *reply {
	if value != nil {
		r.fields = append(r.fields, toon.Field{Key: key, Value: value})
	}
	return r
}

// hint appends next steps, each a complete command an agent can run.
func (r *reply) hint(lines ...string) *reply {
	r.help = append(r.help, lines...)
	return r
}

func (r *reply) toon() string {
	fields := r.fields
	if len(r.help) > 0 {
		fields = append(slices.Clip(fields), toon.Field{Key: "help", Value: r.help})
	}
	out, err := toon.MarshalString(toon.NewObject(fields...))
	if err != nil {
		// Every value a reply holds is a string, number, bool, slice or
		// object of those, which the encoder takes.
		panic(fmt.Sprintf("wakewire: a reply did not encode: %v", err))
	}
	return out + "\n"
}

// nonEmpty is s, or nil for the empty string, so add and obj leave it out.
func nonEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// obj builds a nested ordered object, leaving out nil values.
func obj(kv ...any) toon.Object {
	var o toon.Object
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] != nil {
			o.Fields = append(o.Fields, toon.Field{Key: kv[i].(string), Value: kv[i+1]})
		}
	}
	return o
}

// answer prints a command's result: the JSON value under --json, the reply
// as TOON otherwise. Both go to stdout.
func answer(s streams, cmd *call, v any, r *reply) error {
	if cmd.Bool("json") {
		writeJSON(s.out, v)
		return nil
	}
	_, err := io.WriteString(s.out, r.toon())
	return err
}

// truncateAt is how much of a long text a default answer shows.
const truncateAt = 1000

// clip shortens a long text to truncateAt characters and says how long it
// was; it reports whether it cut anything.
func clip(text string, full bool) (string, bool) {
	n := utf8.RuneCountInString(text)
	if full || n <= truncateAt {
		return text, false
	}
	runes := []rune(text)
	return fmt.Sprintf("%s... (truncated, %d chars total)", string(runes[:truncateAt]), n), true
}

// ago names a past time relative to now, as 15s ago, 4m ago, 3h ago or 2d ago.
func ago(now, t time.Time) string {
	d := now.Sub(t)
	if d < 0 {
		return "in " + span(-d)
	}
	return span(d) + " ago"
}

// ahead names a future time relative to now, as in 30s.
func ahead(now, t time.Time) string {
	if t.Before(now) {
		return "due"
	}
	return "in " + span(t.Sub(now))
}

func span(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// fieldsFlag lets an agent ask a list for more columns than its default.
func fieldsFlag(extra []string) flagSpec {
	return flagSpec{name: "fields", kind: stringFlag, value: "<column>,...", usage: "more columns, comma-separated, from: " + strings.Join(extra, ", ")}
}

// dryWord names what a command did, or under --dry-run what it would do.
func dryWord(dry bool, did, would string) string {
	if dry {
		return would
	}
	return did
}

// withColumns appends to a row the extra columns asked for, in the order
// asked, from cols, which names every extra column the list offers.
func withColumns(row toon.Object, extra []string, cols map[string]any) toon.Object {
	for _, f := range extra {
		row.Fields = append(row.Fields, toon.Field{Key: f, Value: cols[f]})
	}
	return row
}

// pickFields reads --fields against the columns a list offers beyond its default.
func pickFields(cmd *call, extra []string) ([]string, error) {
	raw := cmd.String("fields")
	if raw == "" {
		return nil, nil
	}
	var out []string
	for f := range strings.SplitSeq(raw, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if !slices.Contains(extra, f) {
			return nil, usageError(fmt.Sprintf("--fields %q is not a column of %s", f, cmd.fullName()),
				"its extra columns are "+strings.Join(extra, ", ")+", as `"+cmd.fullName()+" --fields "+extra[0]+"`")
		}
		if !slices.Contains(out, f) { // a column named twice is one column
			out = append(out, f)
		}
	}
	return out, nil
}
