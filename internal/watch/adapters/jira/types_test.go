package jira

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/gperezmz/wakewire/internal/watch/domain"
)

//go:fix inline
func TestLabelChangesAreReadJoinedOrOneToALabel(t *testing.T) {
	joined := history{ID: "10", Author: &person{DisplayName: "Ada"}, Created: "2026-10-16T10:41:07.123+0000", Items: []changelogItem{
		{Field: "labels", FromString: new("type::spec old"), ToString: new("type::spec ready-for-dev")},
	}}
	each := history{ID: "11", Created: "2026-10-16T10:41:07.123+0000", Items: []changelogItem{
		{Field: "labels", FromString: nil, ToString: new("in-progress")},
		{Field: "labels", FromString: new("old"), ToString: new("")},
	}}
	var got []string
	for _, a := range append(joined.activities(nil), each.activities(nil)...) {
		got = append(got, a.ID+" "+string(a.Kind)+" "+a.Label+" "+a.By)
	}
	want := []string{
		"cl:10:0 label_added ready-for-dev Ada",
		"cl:10:1 label_removed old Ada",
		"cl:11:0 label_added in-progress ",
		"cl:11:1 label_removed old ",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestStatusChangesCloseAndReopenByCategory(t *testing.T) {
	statuses := map[string]string{"1": "new", "2": "indeterminate", "3": "done", "name:Done": "done", "name:To Do": "new"}
	kind := func(from, to string, fromName, toName string) domain.Kind {
		h := history{ID: "1", Items: []changelogItem{{Field: "status", From: new(from), To: new(to), FromString: new(fromName), ToString: new(toName)}}}
		if as := h.activities(statuses); len(as) == 1 {
			return as[0].Kind
		}
		return ""
	}
	if k := kind("2", "3", "In Progress", "Done"); k != domain.KindClosed {
		t.Errorf("to done: %q", k)
	}
	if k := kind("3", "1", "Done", "To Do"); k != domain.KindReopened {
		t.Errorf("out of done: %q", k)
	}
	if k := kind("1", "2", "To Do", "In Progress"); k != "" {
		t.Errorf("between open statuses: %q", k)
	}
	if k := kind("9", "9", "Gone", "Done"); k != domain.KindClosed {
		t.Errorf("by name: %q", k)
	}
}

func TestAssignmentAndEditsCarryTheActor(t *testing.T) {
	h := history{ID: "7", Author: &person{AccountID: "a1", DisplayName: "Ada Reviewer"}, Created: "2026-10-16T10:41:07.000+0200", Items: []changelogItem{
		{Field: "assignee", From: new("a1"), To: new("a2")},
		{Field: "summary"}, {Field: "description"}, {Field: "Rank"},
	}}
	as := h.activities(nil)
	if len(as) != 3 || as[0].Kind != domain.KindReassigned || as[0].Assignee != "a2" || as[1].Field != "title" || as[2].Field != "description" {
		t.Fatalf("%+v", as)
	}
	if want := time.Date(2026, 10, 16, 8, 41, 7, 0, time.UTC); !as[0].At.Equal(want) || as[0].By != "Ada Reviewer" {
		t.Fatalf("at %v by %q", as[0].At, as[0].By)
	}
}

func TestIssueBecomesItem(t *testing.T) {
	var i issue
	err := json.Unmarshal([]byte(`{"id":"10001","key":"PROJ-12","fields":{
		"summary":"Export","labels":["b","a"],"updated":"2026-10-16T10:41:07.123+0000",
		"description":{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Export as CSV."}]}]},
		"assignee":{"accountId":"acc-1","displayName":"Ada"},"reporter":{"accountId":"acc-2"},
		"status":{"id":"3","name":"Done","statusCategory":{"key":"done"}},
		"comment":{"total":4,"comments":[]}}}`), &i)
	if err != nil {
		t.Fatal(err)
	}
	want := domain.Item{Ref: "PROJ-12", Kind: domain.ItemIssue, URL: "https://x.atlassian.net/browse/PROJ-12", State: "closed",
		Labels: []string{"a", "b"}, Assignees: []string{"acc-1"}, Author: "acc-2", Title: "Export", Body: "Export as CSV.",
		Comments: 4, UpdatedAt: "2026-10-16T10:41:07.123+0000"}
	if got := i.item("x.atlassian.net"); !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

func TestIssueWithoutAssigneeOrCommentTotal(t *testing.T) {
	var i issue
	if err := json.Unmarshal([]byte(`{"id":"1","key":"P-1","fields":{"summary":"s","description":null,"assignee":null,"reporter":{"accountId":"me"},"status":{"statusCategory":{"key":"new"}}}}`), &i); err != nil {
		t.Fatal(err)
	}
	it := i.item("x")
	if len(it.Assignees) != 0 || it.Comments != -1 || it.State != "open" || it.Body != "" {
		t.Fatalf("%+v", it)
	}
	rules := domain.Rules{Identity: "me", Labels: domain.Labels{domain.MeaningSpec: "spec"}}
	it.Labels = []string{"SPEC"}
	if !rules.InSet(it) {
		t.Fatal("a spec its identity filed, with no assignee, is in the set whatever the label's case")
	}
}
