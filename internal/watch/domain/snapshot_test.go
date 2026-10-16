package domain

import (
	"strings"
	"testing"
	"time"
)

var (
	t0    = time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)
	rules = Rules{
		Identity: "jdoe",
		Aliases:  []string{"jdoe@example.com"},
		Labels:   Labels{MeaningSpec: "type::spec", MeaningReady: "ready-for-dev", MeaningInProgress: "in-progress", MeaningNeedsInput: "needs-input"},
		Marker:   DefaultMarker,
	}
)

func spec() Item {
	return Item{Ref: "#12", Kind: ItemIssue, URL: "https://x/12", State: "open", Labels: []string{"type::spec"},
		Assignees: []string{"jdoe"}, Author: "alice", Title: "Export", Body: "Export as CSV", Comments: 0}
}

func pr() Item {
	return Item{Ref: "PR #41", Kind: ItemPull, URL: "https://x/41", State: "open", Author: "jdoe", Head: "aaa"}
}

func at(m int) time.Time { return t0.Add(time.Duration(m) * time.Minute) }

func kinds(cs []Change) string {
	var out []string
	for _, c := range cs {
		s := string(c.Kind)
		if c.Status == StatusIgnored {
			s += "(ignored)"
		}
		out = append(out, s)
	}
	return strings.Join(out, ",")
}

func TestFirstPollRecordsWhatIsThereAndReportsNothing(t *testing.T) {
	d := Diff(rules, nil, Observation{Item: spec(), At: at(0), Activities: []Activity{
		{ID: "c1", Kind: KindComment, By: "bob", At: at(-5), Body: "old"},
	}}, time.Time{})
	if len(d.Changes) != 0 || d.Drop || d.Next.Baseline != at(0) || len(d.Next.Seen) != 1 {
		t.Fatalf("%+v", d)
	}
}

func TestItemNewAfterTheFirstPollReportsWhatHappenedSince(t *testing.T) {
	d := Diff(rules, nil, Observation{Item: spec(), At: at(10), Activities: []Activity{
		{ID: "e1", Kind: KindLabelAdded, Label: "type::spec", By: "alice", At: at(6)},
		{ID: "c0", Kind: KindComment, By: "bob", At: at(1), Body: "before"},
	}}, at(5))
	if kinds(d.Changes) != "label_added" || d.Changes[0].By != "alice" {
		t.Fatalf("%s %+v", kinds(d.Changes), d.Changes)
	}
}

func TestLabelChangesNameWhoMadeThemAndSkipAgentLabels(t *testing.T) {
	prev := Diff(rules, nil, Observation{Item: spec(), At: at(0)}, time.Time{}).Next
	now := spec()
	now.Labels = []string{"type::spec", "ready-for-dev", "in-progress", "bug"}
	d := Diff(rules, &prev, Observation{Item: now, At: at(2), Activities: []Activity{
		{ID: "e1", Kind: KindLabelAdded, Label: "Ready-For-Dev", By: "alice", At: at(1)},
		{ID: "e2", Kind: KindLabelAdded, Label: "in-progress", By: "jdoe", At: at(1)},
	}}, time.Time{})
	if got := kinds(d.Changes); got != "label_added,label_added(ignored),label_added(ignored)" {
		t.Fatal(got)
	}
	if d.Changes[0].By != "alice" || d.Changes[0].At != at(1) {
		t.Fatalf("%+v", d.Changes[0])
	}
}

func TestCommentsAreNewOnceAndEchoesAreIgnored(t *testing.T) {
	prev := Diff(rules, nil, Observation{Item: spec(), At: at(0)}, time.Time{}).Next
	acts := []Activity{
		{ID: "c1", Kind: KindComment, By: "bob", At: at(1), Body: "please also XLSX"},
		{ID: "c2", Kind: KindComment, By: "jdoe", At: at(1), Body: "on it\n\n<kbd>🤖 unattendish agent</kbd>"},
	}
	now := spec()
	now.Comments = 2
	d := Diff(rules, &prev, Observation{Item: now, At: at(2), Activities: acts}, time.Time{})
	if kinds(d.Changes) != "comment,comment(ignored)" || *d.Changes[0].Body != "please also XLSX" {
		t.Fatalf("%s", kinds(d.Changes))
	}
	again := Diff(rules, &d.Next, Observation{Item: now, At: at(3), Activities: acts}, time.Time{})
	if len(again.Changes) != 0 {
		t.Fatalf("seen comments reported again: %s", kinds(again.Changes))
	}
}

func TestEditIsHeldUntilTheTextHoldsStillThenReportedOnce(t *testing.T) {
	prev := Diff(rules, nil, Observation{Item: spec(), At: at(0)}, time.Time{}).Next
	e1, e2 := spec(), spec()
	e1.Body = "Export as CSV and"
	e2.Body = "Export as CSV and XLSX"
	d1 := Diff(rules, &prev, Observation{Item: e1, At: at(1)}, time.Time{})
	d2 := Diff(rules, &d1.Next, Observation{Item: e2, At: at(2)}, time.Time{})
	if len(d1.Changes)+len(d2.Changes) != 0 {
		t.Fatal("an edit still moving was reported")
	}
	d3 := Diff(rules, &d2.Next, Observation{Item: e2, At: at(3)}, time.Time{})
	if kinds(d3.Changes) != "edited" || d3.Changes[0].Field != "description" {
		t.Fatalf("%s", kinds(d3.Changes))
	}
	if want := "@@ -1,1 +1,1 @@\n-Export as CSV\n+Export as CSV and XLSX"; d3.Changes[0].Diff != want {
		t.Fatalf("diff %q", d3.Changes[0].Diff)
	}
	if d3.Next.Held != nil {
		t.Fatal("held edit kept after report")
	}
}

func TestHeldEditSettlesWhenTheItemIsNotRead(t *testing.T) {
	prev := Diff(rules, nil, Observation{Item: spec(), At: at(0)}, time.Time{}).Next
	e := spec()
	e.Title = "Export v2"
	d := Diff(rules, &prev, Observation{Item: e, At: at(1)}, time.Time{})
	cs, s := Settle(rules, d.Next)
	if kinds(cs) != "edited" || s.Held != nil {
		t.Fatalf("%s", kinds(cs))
	}
}

func TestEditUndoneBeforeItSettlesReportsNothing(t *testing.T) {
	prev := Diff(rules, nil, Observation{Item: spec(), At: at(0)}, time.Time{}).Next
	e := spec()
	e.Title = "oops"
	d1 := Diff(rules, &prev, Observation{Item: e, At: at(1)}, time.Time{})
	d2 := Diff(rules, &d1.Next, Observation{Item: spec(), At: at(2)}, time.Time{})
	d3 := Diff(rules, &d2.Next, Observation{Item: spec(), At: at(3)}, time.Time{})
	if len(d2.Changes)+len(d3.Changes) != 0 {
		t.Fatalf("%s %s", kinds(d2.Changes), kinds(d3.Changes))
	}
}

func TestReassignedAwayLeavesTheSetAfterItIsRecorded(t *testing.T) {
	prev := Diff(rules, nil, Observation{Item: spec(), At: at(0)}, time.Time{}).Next
	now := spec()
	now.Assignees = []string{"carol"}
	d := Diff(rules, &prev, Observation{Item: now, At: at(1), Activities: []Activity{
		{ID: "a1", Kind: KindReassigned, By: "alice", At: at(1)},
	}}, time.Time{})
	if kinds(d.Changes) != "reassigned" || !d.Drop || d.Changes[0].By != "alice" {
		t.Fatalf("%s drop=%v", kinds(d.Changes), d.Drop)
	}
}

func TestMergeIsOneChangeNotAlsoClosed(t *testing.T) {
	prev := Diff(rules, nil, Observation{Item: pr(), At: at(0)}, time.Time{}).Next
	now := pr()
	now.State, now.MergedAt = "closed", "2026-10-16T09:05:00Z"
	d := Diff(rules, &prev, Observation{Item: now, At: at(6), Activities: []Activity{
		{ID: "m", Kind: KindMerged, By: "alice", At: at(5)},
		{ID: "cl", Kind: KindClosed, By: "alice", At: at(5)},
	}}, time.Time{})
	if kinds(d.Changes) != "merged" {
		t.Fatal(kinds(d.Changes))
	}
}

func TestPushWakesOnlyForSomeoneElsesCommits(t *testing.T) {
	prev := Diff(rules, nil, Observation{Item: pr(), At: at(0)}, time.Time{}).Next
	now := pr()
	now.Head = "bbb"
	own := Diff(rules, &prev, Observation{Item: now, At: at(1), Activities: []Activity{
		{ID: "k1", Kind: KindPushed, At: at(1), Commit: &Commit{ID: "bbb", Author: "jdoe@example.com"}},
	}}, time.Time{})
	if kinds(own.Changes) != "pushed(ignored)" {
		t.Fatal(kinds(own.Changes))
	}
	now.Head = "ccc"
	theirs := Diff(rules, &own.Next, Observation{Item: now, At: at(2), Activities: []Activity{
		{ID: "k1", Kind: KindPushed, At: at(1), Commit: &Commit{ID: "bbb", Author: "jdoe@example.com"}},
		{ID: "k2", Kind: KindPushed, At: at(2), Commit: &Commit{ID: "ccc", Author: "alice"}},
	}}, time.Time{})
	if kinds(theirs.Changes) != "pushed" || len(theirs.Changes[0].Commits) != 1 {
		t.Fatalf("%s %+v", kinds(theirs.Changes), theirs.Changes)
	}
}

func TestLabelAddedAndRemovedBetweenReadsIsStillReported(t *testing.T) {
	prev := Diff(rules, nil, Observation{Item: spec(), At: at(0)}, time.Time{}).Next
	d := Diff(rules, &prev, Observation{Item: spec(), At: at(3), Activities: []Activity{
		{ID: "e1", Kind: KindLabelAdded, Label: "ready-for-dev", By: "alice", At: at(1)},
		{ID: "e2", Kind: KindLabelRemoved, Label: "ready-for-dev", By: "alice", At: at(2)},
	}}, time.Time{})
	if kinds(d.Changes) != "label_added,label_removed" {
		t.Fatal(kinds(d.Changes))
	}
}

func TestBodyIsCutAtTheLimit(t *testing.T) {
	c := Change{}.WithBody(strings.Repeat("é", BodyLimit+5))
	if len([]rune(*c.Body)) != BodyLimit || !*c.Truncated {
		t.Fatal("not cut")
	}
}

func TestPartition(t *testing.T) {
	filed := spec()
	filed.Assignees, filed.Author = nil, "jdoe"
	other := spec()
	other.Assignees = []string{"carol"}
	unlabeled := spec()
	unlabeled.Labels = []string{"bug"}
	theirPR := pr()
	theirPR.Author = "alice"
	for name, c := range map[string]struct {
		it   Item
		want bool
	}{"assigned": {spec(), true}, "filed, unassigned": {filed, true}, "someone else's": {other, false},
		"not a spec": {unlabeled, false}, "own PR": {pr(), true}, "their PR": {theirPR, false}} {
		if got := rules.InSet(c.it); got != c.want {
			t.Errorf("%s: %v", name, got)
		}
	}
}
