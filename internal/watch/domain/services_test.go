package domain

import (
	"errors"
	"testing"
)

// The section as set-up-unattendish writes it, Services and Labels each
// under a subtitle.
const servicesMD = "# Project\n\n## Forge and tracker\n\n### Services\n\n| Role | Service | Scope | Tool |\n| --- | --- | --- | --- |\n| tracker | Jira | `acme.atlassian.net/PROJ` | its MCP server |\n| forge | GitHub | `o/x` | `gh` |\n\n### Labels\n\n| Meaning | Label |\n| --- | --- |\n| spec | `spec` |\n| ready | `ready` |\n| in progress | `in-progress` |\n| needs input | `needs-input` |\n\n## Other\n\n| Role | Service | Scope | Tool |\n| --- | --- | --- | --- |\n| tracker | Linear | `ENG` | x |\n"

func TestTheServicesTableIsReadUnderItsSubtitle(t *testing.T) {
	r, ok := ReadSource(DefaultTemplate(), "AGENTS.md", servicesMD)
	if !ok {
		t.Fatal("no section")
	}
	if r.TrackerRow != (ServiceRow{Service: "Jira", Scope: "acme.atlassian.net/PROJ", Line: 9}) {
		t.Errorf("tracker row: %+v", r.TrackerRow)
	}
	if r.ForgeRow != (ServiceRow{Service: "GitHub", Scope: "o/x", Line: 10}) {
		t.Errorf("forge row: %+v", r.ForgeRow)
	}
	if len(r.Labels.Missing()) != 0 || r.Labels[MeaningSpec] != "spec" || r.LabelLines[MeaningNeedsInput] != 19 {
		t.Errorf("labels: %v %v", r.Labels, r.LabelLines)
	}
	if r.TrackerLine != 0 {
		t.Errorf("tracker line: %d", r.TrackerLine)
	}
}

func row(service, scope string) SourceRead {
	return SourceRead{File: "AGENTS.md", TrackerRow: ServiceRow{Service: service, Scope: scope, Line: 9}, ForgeRow: ServiceRow{Service: service, Scope: scope, Line: 10}}
}

func TestTheTrackerRowNamesTheTracker(t *testing.T) {
	gh := Repo{Host: "github.com", Path: "o/x"}
	gl := Repo{Host: "gitlab.example.com", Path: "g/sub/p"}
	for _, c := range []struct {
		read        SourceRead
		repo        Repo
		forge, want string
		refused     bool
	}{
		{row("GitHub", "o/x"), gh, "GitHub", "github-issues", false},
		{row("github", "O/X"), gh, "GitHub", "github-issues", false},
		{row("GitLab", "g/sub/p"), gl, "GitLab", "gitlab-issues", false},
		{row("Jira", "Acme.atlassian.net/PROJ"), gh, "GitHub", "jira:acme.atlassian.net/PROJ", false},
		{row("GitHub", "o/other"), gh, "GitHub", "", true}, // another repository's issues
		{row("GitLab", "o/x"), gh, "GitHub", "", true},     // not the forge
		{row("Jira", "PROJ"), gh, "GitHub", "", true},      // no site
		{row("Linear", "ENG"), gh, "GitHub", "", true},
	} {
		got, err := TrackerFrom(c.read, c.repo, c.forge)
		if c.refused {
			if code(err) != "source_incomplete" {
				t.Errorf("%+v: want a refusal, got %v %v", c.read.TrackerRow, got, err)
			}
			continue
		}
		if err != nil || got.String() != c.want {
			t.Errorf("%+v: %v %v, want %s", c.read.TrackerRow, got, err, c.want)
		}
	}
}

func TestTheForgeRowIsTheRepositoryWatched(t *testing.T) {
	gh := Repo{Host: "github.com", Path: "o/x"}
	for _, c := range []struct {
		read    SourceRead
		refused bool
	}{
		{row("GitHub", "o/x"), false},
		{SourceRead{}, false}, // no forge row
		{row("none", "—"), true},
		{row("GitLab", "o/x"), true},
		{row("GitHub", "o/y"), true},
	} {
		err := CheckForge(c.read, gh, "GitHub")
		if (err != nil) != c.refused || (err != nil && code(err) != "source_incomplete") {
			t.Errorf("%+v: %v", c.read.ForgeRow, err)
		}
	}
}

func code(err error) string {
	if r, ok := errors.AsType[*Refusal](err); ok {
		return r.Code()
	}
	return ""
}
