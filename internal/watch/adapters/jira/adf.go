package jira

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// node is one node of an Atlassian Document Format document.
type node struct {
	Type    string         `json:"type"`
	Text    string         `json:"text"`
	Attrs   map[string]any `json:"attrs"`
	Marks   []mark         `json:"marks"`
	Content []node         `json:"content"`
}

type mark struct {
	Type  string         `json:"type"`
	Attrs map[string]any `json:"attrs"`
}

func (n node) attr(key string) string {
	switch v := n.Attrs[key].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

// markdownOf converts a description or comment body to Markdown. A body is
// a document in Atlassian Document Format; a plain string, which the
// older API versions gave, is returned as it is; null is empty.
func markdownOf(raw json.RawMessage) string {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	if raw[0] == '"' {
		var s string
		_ = json.Unmarshal(raw, &s)
		return s
	}
	var doc node
	if err := json.Unmarshal(raw, &doc); err != nil {
		return ""
	}
	return strings.TrimSpace(blocks(doc.Content, "\n\n"))
}

// blocks renders block nodes, apart by sep.
func blocks(ns []node, sep string) string {
	var parts []string
	for _, n := range ns {
		if s := block(n); s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, sep)
}

func block(n node) string {
	switch n.Type {
	case "paragraph":
		return inline(n.Content)
	case "heading":
		level, _ := strconv.Atoi(n.attr("level"))
		level = min(max(level, 1), 6)
		return strings.Repeat("#", level) + " " + inline(n.Content)
	case "bulletList", "orderedList", "taskList":
		return list(n)
	case "codeBlock":
		return "```" + n.attr("language") + "\n" + plain(n) + "\n```"
	case "blockquote":
		return prefixLines(blocks(n.Content, "\n\n"), "> ", "> ")
	case "rule":
		return "---"
	case "table":
		return table(n)
	case "mediaSingle", "mediaGroup":
		return blocks(n.Content, "\n")
	case "media":
		if alt := n.attr("alt"); alt != "" {
			return "[attachment: " + alt + "]"
		}
		return "[attachment]"
	case "text", "hardBreak", "mention", "emoji", "inlineCard", "status", "date":
		return inline([]node{n})
	}
	// An unknown node: its content, block by block when it holds blocks,
	// as text otherwise.
	if len(n.Content) == 0 {
		return n.Text
	}
	if isInline(n.Content[0]) {
		return inline(n.Content)
	}
	return blocks(n.Content, "\n\n")
}

func isInline(n node) bool {
	switch n.Type {
	case "text", "hardBreak", "mention", "emoji", "inlineCard", "status", "date":
		return true
	}
	return false
}

func list(n node) string {
	order := 1
	if v, err := strconv.Atoi(n.attr("order")); err == nil {
		order = v
	}
	var items []string
	for _, it := range n.Content {
		var lead string
		switch n.Type {
		case "orderedList":
			lead = strconv.Itoa(order) + ". "
			order++
		case "taskList":
			lead = "- [ ] "
			if strings.EqualFold(it.attr("state"), "DONE") {
				lead = "- [x] "
			}
		default:
			lead = "- "
		}
		body := blocks(it.Content, "\n")
		if len(it.Content) > 0 && isInline(it.Content[0]) { // a taskItem holds inline content
			body = inline(it.Content)
		}
		items = append(items, prefixLines(body, lead, strings.Repeat(" ", len(lead))))
	}
	return strings.Join(items, "\n")
}

func table(n node) string {
	var rows [][]string
	for _, r := range n.Content {
		var cells []string
		for _, c := range r.Content {
			cells = append(cells, strings.ReplaceAll(strings.ReplaceAll(blocks(c.Content, " "), "\n", " "), "|", `\|`))
		}
		rows = append(rows, cells)
	}
	if len(rows) == 0 {
		return ""
	}
	var out []string
	for i, cells := range rows {
		out = append(out, "| "+strings.Join(cells, " | ")+" |")
		if i == 0 {
			out = append(out, "|"+strings.Repeat(" --- |", len(cells)))
		}
	}
	return strings.Join(out, "\n")
}

// prefixLines puts first before the first line and rest before the others.
func prefixLines(s, first, rest string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		p := rest
		if i == 0 {
			p = first
		}
		if l == "" {
			p = strings.TrimRight(p, " ")
		}
		lines[i] = p + l
	}
	return strings.Join(lines, "\n")
}

func inline(ns []node) string {
	var b strings.Builder
	for _, n := range ns {
		switch n.Type {
		case "text":
			b.WriteString(marked(n))
		case "hardBreak":
			b.WriteString("\n")
		case "mention":
			name := strings.TrimPrefix(n.attr("text"), "@")
			if name == "" {
				name = n.attr("id")
			}
			b.WriteString("@" + name)
		case "emoji":
			if t := n.attr("text"); t != "" {
				b.WriteString(t)
			} else {
				b.WriteString(n.attr("shortName"))
			}
		case "inlineCard":
			b.WriteString(n.attr("url"))
		case "status":
			b.WriteString(n.attr("text"))
		case "date":
			if ms, err := strconv.ParseInt(n.attr("timestamp"), 10, 64); err == nil {
				b.WriteString(time.UnixMilli(ms).UTC().Format("2006-01-02"))
			}
		default:
			b.WriteString(block(n))
		}
	}
	return b.String()
}

// marked wraps a text node's text in its marks. Space at either end stays
// outside, since Markdown does not close emphasis after a space.
func marked(n node) string {
	text := n.Text
	core := strings.TrimFunc(text, unicode.IsSpace)
	if core == "" {
		return text
	}
	lead := text[:strings.Index(text, core)]
	trail := text[len(lead)+len(core):]
	for _, m := range n.Marks {
		if m.Type == "code" {
			core = "`" + core + "`"
		}
	}
	for _, m := range n.Marks {
		switch m.Type {
		case "strong":
			core = "**" + core + "**"
		case "em":
			core = "*" + core + "*"
		case "strike":
			core = "~~" + core + "~~"
		case "link":
			if href, _ := m.Attrs["href"].(string); href != "" {
				core = "[" + core + "](" + href + ")"
			}
		}
	}
	return lead + core + trail
}

// plain is a node's text with no marks, block children apart by lines.
func plain(n node) string {
	if len(n.Content) == 0 {
		return n.Text
	}
	var parts []string
	for _, c := range n.Content {
		parts = append(parts, plain(c))
	}
	if isInline(n.Content[0]) {
		return strings.Join(parts, "")
	}
	return strings.Join(parts, "\n")
}
