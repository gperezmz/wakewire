package domain

import (
	"regexp"
	"strings"
)

// Template says where in a repository a watch's tracker and labels are
// read; see configuration.md, "The source template".
type Template struct {
	Files      []string
	Heading    string
	Tracker    string // the prefix of the line naming the tracker
	TableKey   string
	TableValue string
	Keys       map[Meaning]string // the table key for each meaning
}

// DefaultTemplate reads the section set-up-unattendish writes.
func DefaultTemplate() Template {
	return Template{
		Files:      []string{"AGENTS.md", "CLAUDE.md"},
		Heading:    "Forge and tracker",
		Tracker:    "Tracker:",
		TableKey:   "Meaning",
		TableValue: "Label",
		Keys: map[Meaning]string{
			MeaningSpec: "spec", MeaningReady: "ready",
			MeaningInProgress: "in progress", MeaningNeedsInput: "needs input",
		},
	}
}

// SourceRead is what the template found in one file, with the lines it
// found each value on, 1-based; a line of 0 means not found.
type SourceRead struct {
	File        string
	Heading     string
	HeadingLine int
	Tracker     string // as written; parsed by the caller with ParseTracker
	TrackerLine int
	Labels      Labels
	LabelLines  map[Meaning]int
}

var headingLine = regexp.MustCompile(`^(#{1,6})\s+(.*?)\s*#*\s*$`)

// ReadSource finds the template's section in one file's text. found is
// false when the file has no such section.
func ReadSource(t Template, file, text string) (SourceRead, bool) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	r := SourceRead{File: file, Labels: Labels{}, LabelLines: map[Meaning]int{}}
	level := 0
	inFence := false
	var keyCol, valCol = -1, -1
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if m := headingLine.FindStringSubmatch(line); m != nil {
			if level > 0 && len(m[1]) <= level {
				break // the section ended
			}
			if level == 0 && strings.EqualFold(normalizeLine(m[2]), normalizeLine(t.Heading)) {
				level, r.Heading, r.HeadingLine = len(m[1]), m[2], i+1
			}
			continue
		}
		if level == 0 {
			continue
		}
		plain := normalizeLine(line)
		plain = strings.TrimSpace(strings.TrimLeft(plain, "-+ "))
		if r.TrackerLine == 0 && t.Tracker != "" && strings.HasPrefix(strings.ToLower(plain), strings.ToLower(t.Tracker)) {
			// The value is read from the raw line, so that backticks or
			// emphasis around it do not lose the characters inside them.
			idx := strings.Index(strings.ToLower(raw), strings.ToLower(t.Tracker))
			if idx < 0 {
				raw = plain[len(t.Tracker):]
			} else {
				raw = raw[idx+len(t.Tracker):]
			}
			r.Tracker = strings.Trim(strings.TrimSpace(raw), "`*_ ")
			r.TrackerLine = i + 1
			continue
		}
		if !strings.HasPrefix(line, "|") {
			keyCol, valCol = -1, -1
			continue
		}
		cells := tableCells(line)
		if keyCol < 0 {
			for j, c := range cells {
				switch {
				case strings.EqualFold(c, t.TableKey):
					keyCol = j
				case strings.EqualFold(c, t.TableValue):
					valCol = j
				}
			}
			if keyCol < 0 || valCol < 0 {
				keyCol, valCol = -1, -1
			}
			continue
		}
		if isSeparator(cells) || keyCol >= len(cells) || valCol >= len(cells) {
			continue
		}
		key, val := cells[keyCol], strings.Trim(cells[valCol], "` ")
		for m, want := range t.Keys {
			if strings.EqualFold(normalizeLine(key), want) && val != "" && r.LabelLines[m] == 0 {
				r.Labels[m], r.LabelLines[m] = val, i+1
			}
		}
	}
	return r, level > 0
}

func tableCells(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	parts := strings.Split(line, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func isSeparator(cells []string) bool {
	for _, c := range cells {
		if strings.Trim(c, ":- ") != "" {
			return false
		}
	}
	return true
}
