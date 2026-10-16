package jira

import (
	"encoding/json"
	"testing"
)

func TestMarkdownOf(t *testing.T) {
	cases := []struct{ name, adf, want string }{
		{"null", `null`, ""},
		{"plain string", `"just text"`, "just text"},
		{"paragraphs", `{"type":"doc","version":1,"content":[
			{"type":"paragraph","content":[{"type":"text","text":"One."}]},
			{"type":"paragraph","content":[{"type":"text","text":"Two."}]}]}`, "One.\n\nTwo."},
		{"marks", `{"type":"doc","content":[{"type":"paragraph","content":[
			{"type":"text","text":"a "},
			{"type":"text","text":"bold ","marks":[{"type":"strong"}]},
			{"type":"text","text":"it","marks":[{"type":"em"}]},
			{"type":"text","text":" "},
			{"type":"text","text":"code","marks":[{"type":"code"}]},
			{"type":"text","text":" "},
			{"type":"text","text":"gone","marks":[{"type":"strike"}]},
			{"type":"text","text":" "},
			{"type":"text","text":"site","marks":[{"type":"link","attrs":{"href":"https://example.com"}}]},
			{"type":"text","text":" "},
			{"type":"text","text":"both","marks":[{"type":"code"},{"type":"link","attrs":{"href":"https://example.com/x"}}]}]}]}`,
			"a **bold** *it* `code` ~~gone~~ [site](https://example.com) [`both`](https://example.com/x)"},
		{"heading and rule", `{"type":"doc","content":[
			{"type":"heading","attrs":{"level":2},"content":[{"type":"text","text":"Acceptance"}]},
			{"type":"rule"},
			{"type":"paragraph","content":[{"type":"text","text":"x"}]}]}`, "## Acceptance\n\n---\n\nx"},
		{"lists", `{"type":"doc","content":[
			{"type":"bulletList","content":[
				{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"CSV"}]},
					{"type":"bulletList","content":[{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"UTF-8"}]}]}]}]},
				{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"XLSX"}]}]}]},
			{"type":"orderedList","attrs":{"order":3},"content":[
				{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"c"}]}]},
				{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"d"}]}]}]}]}`,
			"- CSV\n  - UTF-8\n- XLSX\n\n3. c\n4. d"},
		{"code block", `{"type":"doc","content":[{"type":"codeBlock","attrs":{"language":"go"},"content":[{"type":"text","text":"x := 1"}]}]}`,
			"```go\nx := 1\n```"},
		{"blockquote", `{"type":"doc","content":[{"type":"blockquote","content":[
			{"type":"paragraph","content":[{"type":"text","text":"a"}]},
			{"type":"paragraph","content":[{"type":"text","text":"b"}]}]}]}`, "> a\n>\n> b"},
		{"mention and break", `{"type":"doc","content":[{"type":"paragraph","content":[
			{"type":"mention","attrs":{"id":"abc","text":"@Ada Reviewer"}},
			{"type":"text","text":" please look"},
			{"type":"hardBreak"},
			{"type":"text","text":"thanks"}]}]}`, "@Ada Reviewer please look\nthanks"},
		{"mention without text", `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"mention","attrs":{"id":"abc"}}]}]}`, "@abc"},
		{"unknown node falls back to its text", `{"type":"doc","content":[{"type":"panel","attrs":{"panelType":"info"},"content":[
			{"type":"paragraph","content":[{"type":"text","text":"careful"}]}]},
			{"type":"somethingNew","content":[{"type":"text","text":"still here"}]}]}`, "careful\n\nstill here"},
		{"marker line", `{"type":"doc","content":[
			{"type":"paragraph","content":[{"type":"text","text":"Started."}]},
			{"type":"paragraph","content":[{"type":"text","text":"🤖 unattendish agent"}]}]}`, "Started.\n\n🤖 unattendish agent"},
		{"table", `{"type":"doc","content":[{"type":"table","content":[
			{"type":"tableRow","content":[{"type":"tableHeader","content":[{"type":"paragraph","content":[{"type":"text","text":"a"}]}]},{"type":"tableHeader","content":[{"type":"paragraph","content":[{"type":"text","text":"b"}]}]}]},
			{"type":"tableRow","content":[{"type":"tableCell","content":[{"type":"paragraph","content":[{"type":"text","text":"1"}]}]},{"type":"tableCell","content":[{"type":"paragraph","content":[{"type":"text","text":"2"}]}]}]}]}]}`,
			"| a | b |\n| --- | --- |\n| 1 | 2 |"},
	}
	for _, c := range cases {
		if got := markdownOf(json.RawMessage(c.adf)); got != c.want {
			t.Errorf("%s:\ngot  %q\nwant %q", c.name, got, c.want)
		}
	}
}
