package domain

import (
	"slices"
	"testing"
)

func TestASpecAssignedToTheIdentityIsReportedAsReassignedNotAsLabelAdded(t *testing.T) {
	// The spec label was there already; the identity was assigned after
	// the last poll, which is how the item came into its partition.
	d := Diff(rules, nil, Observation{Item: spec(), At: at(10), Activities: []Activity{
		{ID: "a0", Kind: KindReassigned, Assignee: "carol", By: "alice", At: at(2)},
		{ID: "a1", Kind: KindReassigned, Assignee: "jdoe", By: "alice", At: at(7)},
	}}, at(5))
	if kinds(d.Changes) != "reassigned" {
		t.Fatalf("%s %+v", kinds(d.Changes), d.Changes)
	}
	c := d.Changes[0]
	if c.By != "alice" || c.At != at(7) || len(c.From) != 0 || !slices.Equal(c.To, []string{"jdoe"}) {
		t.Fatalf("%+v", c)
	}
	if c.From == nil {
		t.Fatal("from must be an empty list, not absent")
	}
}

func TestAnAssignmentThatNamesNoOneStillExplainsTheEntry(t *testing.T) {
	// GitLab's system note does not say whom it assigned.
	d := Diff(rules, nil, Observation{Item: spec(), At: at(10), Activities: []Activity{
		{ID: "n1", Kind: KindReassigned, By: "alice", At: at(7)},
	}}, at(5))
	if kinds(d.Changes) != "reassigned" {
		t.Fatalf("%s", kinds(d.Changes))
	}
}

func TestAnAssignmentToSomeoneElseDoesNotExplainTheEntry(t *testing.T) {
	// Another person's assignment on an item the identity is in through
	// the item's fields alone: nothing in the timeline explains the entry.
	d := Diff(rules, nil, Observation{Item: spec(), At: at(10), Activities: []Activity{
		{ID: "a1", Kind: KindReassigned, Assignee: "carol", By: "alice", At: at(7)},
	}}, at(5))
	if kinds(d.Changes) != "label_added" {
		t.Fatalf("%s", kinds(d.Changes))
	}
}

func TestASpecWithNothingInItsTimelineStillEntersAsLabelAdded(t *testing.T) {
	d := Diff(rules, nil, Observation{Item: spec(), At: at(10)}, at(5))
	if kinds(d.Changes) != "label_added" || d.Changes[0].Label != "type::spec" {
		t.Fatalf("%s %+v", kinds(d.Changes), d.Changes)
	}
}

func TestTheSpecLabelAddedAndTheAssignmentAreBothReported(t *testing.T) {
	d := Diff(rules, nil, Observation{Item: spec(), At: at(10), Activities: []Activity{
		{ID: "e1", Kind: KindLabelAdded, Label: "type::spec", By: "alice", At: at(6)},
		{ID: "a1", Kind: KindReassigned, Assignee: "jdoe", By: "alice", At: at(7)},
	}}, at(5))
	if kinds(d.Changes) != "label_added,reassigned" {
		t.Fatalf("%s", kinds(d.Changes))
	}
}
