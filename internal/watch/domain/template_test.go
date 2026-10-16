package domain

import "testing"

const agentsMD = "# Project\n\nSome text.\n\n## Forge and tracker\n\nWe use Jira.\n\nTracker: jira:acme.atlassian.net/PROJ\n\n| Meaning | Label |\n| --- | --- |\n| spec | `type::spec` |\n| ready | `ready-for-dev` |\n| In Progress | `in-progress` |\n| needs input | `needs-input` |\n\n## Other\n\n| Meaning | Label |\n| --- | --- |\n| spec | `wrong` |\n"

func TestReadsTheDefaultSection(t *testing.T) {
	r, ok := ReadSource(DefaultTemplate(), "AGENTS.md", agentsMD)
	if !ok || r.HeadingLine != 5 || r.Tracker != "jira:acme.atlassian.net/PROJ" || r.TrackerLine != 9 {
		t.Fatalf("%+v", r)
	}
	want := Labels{MeaningSpec: "type::spec", MeaningReady: "ready-for-dev", MeaningInProgress: "in-progress", MeaningNeedsInput: "needs-input"}
	for m, l := range want {
		if r.Labels[m] != l {
			t.Errorf("%s: %q", m, r.Labels[m])
		}
	}
	if r.LabelLines[MeaningSpec] != 13 || len(r.Labels.Missing()) != 0 {
		t.Fatalf("%v", r.LabelLines)
	}
}

func TestNoSectionIsNotFound(t *testing.T) {
	if _, ok := ReadSource(DefaultTemplate(), "AGENTS.md", "# x\n\n## Elsewhere\n"); ok {
		t.Fatal("found")
	}
}

func TestSectionWithoutTrackerReportsItMissing(t *testing.T) {
	r, ok := ReadSource(DefaultTemplate(), "AGENTS.md", "### FORGE AND TRACKER\n\n| Meaning | Label |\n|-|-|\n| spec | s |\n")
	if !ok || r.TrackerLine != 0 || r.Labels[MeaningSpec] != "s" || len(r.Labels.Missing()) != 3 {
		t.Fatalf("%+v", r)
	}
}

func TestTrackerParsing(t *testing.T) {
	for in, ok := range map[string]bool{"github-issues": true, "`gitlab-issues`": true, "jira:x.atlassian.net/KAN": true, "jira:x/kan": false, "linear": false} {
		if _, err := ParseTracker(in); (err == nil) != ok {
			t.Errorf("%s: %v", in, err)
		}
	}
}

func TestTrackerLineWithEmphasis(t *testing.T) {
	r, _ := ReadSource(DefaultTemplate(), "AGENTS.md", "## Forge and tracker\n\n- **Tracker:** `jira:a.atlassian.net/KAN`\n")
	if r.Tracker != "jira:a.atlassian.net/KAN" {
		t.Fatalf("%q", r.Tracker)
	}
}
