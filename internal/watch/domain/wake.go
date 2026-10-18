package domain

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// Wake writes the message that tells a session what changed. It carries
// facts only: the item, the kind, the login, never a title or anything a
// person wrote; see the wake contract. Its first and last lines are
// wakewire's own fixed words.
func Wake(batch int64, changes []Change) string {
	if len(changes) == 0 {
		return ""
	}
	var problems, rest []Change
	for _, c := range changes {
		if c.Kind == KindProblem {
			problems = append(problems, c)
		} else {
			rest = append(rest, c)
		}
	}
	if len(rest) == 0 {
		var b strings.Builder
		for _, p := range problems {
			fmt.Fprintf(&b, "[wakewire] %s Run `wakewire status` for the fix.\n", sentence(p.Text))
		}
		return strings.TrimSuffix(b.String(), "\n")
	}
	var b strings.Builder
	b.WriteString("[wakewire] New activity:\n")
	lines := make([]string, 0, len(problems))
	for _, p := range problems {
		lines = append(lines, "- "+sentence(p.Text))
	}
	lines = append(lines, grouped(rest)...)
	if len(lines) > MaxWakeLines {
		lines = append(lines[:len(problems)], perItem(rest)...)
	}
	if len(lines) > MaxWakeLines {
		more := 0
		for _, l := range lines[MaxWakeLines-1:] {
			more += countOf(l)
		}
		lines = append(lines[:MaxWakeLines-1], fmt.Sprintf("- and %d more", more))
	}
	for _, l := range lines {
		b.WriteString(stripCount(l) + "\n")
	}
	fmt.Fprintf(&b, "Read batch %d with `wakewire changes %d` before acting on it. If none of it needs you, a one-line reply is enough.", batch, batch)
	return b.String()
}

// sentence starts a problem's text with a capital letter.
func sentence(s string) string {
	r := []rune(s)
	if len(r) > 0 {
		r[0] = unicode.ToUpper(r[0])
	}
	return string(r)
}

// Lines carry their change count after a tab, for "and N more"; the tab
// and count are stripped before the line is written.
func stripCount(l string) string { s, _, _ := strings.Cut(l, "\t"); return s }

func countOf(l string) int {
	_, n, ok := strings.Cut(l, "\t")
	if !ok {
		return 1
	}
	var c int
	_, _ = fmt.Sscan(n, &c)
	return c
}

type groupKey struct {
	item string
	kind Kind
}

// grouped counts changes to one item and kind on one line.
func grouped(cs []Change) []string {
	var order []groupKey
	groups := map[groupKey][]Change{}
	for _, c := range cs {
		k := groupKey{c.Item, c.Kind}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], c)
	}
	var lines []string
	for _, k := range order {
		g := groups[k]
		lines = append(lines, fmt.Sprintf("- %s: %s\t%d", k.item, phrase(k.kind, g), len(g)))
	}
	return lines
}

func perItem(cs []Change) []string {
	var order []string
	n := map[string]int{}
	for _, c := range cs {
		if n[c.Item] == 0 {
			order = append(order, c.Item)
		}
		n[c.Item]++
	}
	var lines []string
	for _, it := range order {
		lines = append(lines, fmt.Sprintf("- %s: %d %s\t%d", it, n[it], plural(n[it], "change", "changes"), n[it]))
	}
	return lines
}

func phrase(k Kind, g []Change) string {
	by := byline(g)
	one := len(g) == 1
	c := g[0]
	switch k {
	case KindComment:
		return counted(len(g), "comment", "comments") + by
	case KindInlineComment:
		return counted(len(g), "inline comment", "inline comments") + by
	case KindReview:
		if one {
			s := "review, " + strings.ReplaceAll(safe(c.State, "submitted"), "_", " ")
			if by != "" {
				s += "," + by
			}
			return s
		}
		return counted(len(g), "review", "reviews") + by
	case KindLabelAdded, KindLabelRemoved:
		verb := "added"
		if k == KindLabelRemoved {
			verb = "removed"
		}
		var labels []string
		for _, c := range g {
			labels = append(labels, fmt.Sprintf("%q", safe(c.Label, "?")))
		}
		return fmt.Sprintf("%s %s %s%s", plural(len(g), "label", "labels"), strings.Join(labels, ", "), verb, by)
	case KindClosed, KindReopened, KindMerged:
		return string(k) + by
	case KindReassigned:
		// Who it went from and to is in the batch: an account ID, as Jira
		// names people, is no word for a wake.
		if len(g[len(g)-1].To) == 0 {
			return "unassigned" + by
		}
		return "reassigned" + by
	case KindEdited:
		var fields []string
		for _, c := range g {
			if !slices.Contains(fields, c.Field) {
				fields = append(fields, c.Field)
			}
		}
		return strings.Join(fields, " and ") + " edited" + by
	case KindPushed:
		n := 0
		var authors []string
		for _, c := range g {
			n += len(c.Commits)
			for _, cm := range c.Commits {
				if a := safe(cm.Author, ""); a != "" && !slices.Contains(authors, a) {
					authors = append(authors, a)
				}
			}
		}
		s := "pushed " + counted(n, "commit", "commits")
		if len(authors) > 0 {
			s += " by " + strings.Join(authors, ", ")
		}
		return s
	}
	return string(k)
}

func byline(g []Change) string {
	var names []string
	for _, c := range g {
		if n := safe(c.By, ""); n != "" && !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	switch {
	case len(names) == 0:
		return ""
	case len(names) <= 3:
		return " by " + strings.Join(names, ", ")
	}
	return fmt.Sprintf(" by %d people", len(names))
}

// handle is what a login, a label or a state may hold to appear in a wake.
var handle = regexp.MustCompile(`^[\p{L}\p{N}._:@/+-]{1,40}$`)

// safe keeps a name in the wake only as one plain word. A name a person
// chose freely, such as a Jira display name, could otherwise carry a
// sentence into the session in its user's voice; the batch has it whole.
func safe(s, fallback string) string {
	fields := strings.Fields(s)
	if len(fields) > 0 && handle.MatchString(fields[0]) {
		return fields[0]
	}
	return fallback
}

func counted(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return fmt.Sprintf("%d %s", n, many)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
