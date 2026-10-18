package sources_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gperezmz/wakewire/internal/adapters/home"
	"github.com/gperezmz/wakewire/internal/adapters/httpread"
	"github.com/gperezmz/wakewire/internal/watch/adapters/accounts"
	"github.com/gperezmz/wakewire/internal/watch/adapters/sources"
	"github.com/gperezmz/wakewire/internal/watch/application"
	"github.com/gperezmz/wakewire/internal/watch/domain"
	"github.com/gperezmz/wakewire/internal/wiring"
)

const config = `
[[accounts]]
name   = "gh"
host   = "github.com"
owners = ["wwuser"]
token  = "env:WW_GH"

[[accounts]]
name  = "jira"
host  = "wwsite.atlassian.net"
email = "me@example.com"
token = "env:WW_JIRA"
`

func newSources(t *testing.T) sources.Sources {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"WAKEWIRE_HOME": dir, "WW_GH": "gh-token", "WW_JIRA": "jira-token"}
	paths, err := home.Resolve(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	svc, err := wiring.NewAccounts(paths, func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	return sources.Sources{Accounts: accounts.Accounts{Service: svc}, HTTP: httpread.New("test", nil)}
}

func TestAJiraTrackerGetsASourceOfItsOwn(t *testing.T) {
	repo, _ := domain.ParseRepo("github.com/wwuser/x")
	labels := domain.Labels{domain.MeaningSpec: "type::spec"}
	w := application.Watch{Repo: repo, Identity: "wwuser", ForgeAccount: "gh", Labels: labels, Marker: "m",
		Tracker: domain.Tracker{Kind: domain.TrackerJira, Site: "wwsite.atlassian.net", Project: "KAN"}, TrackerIdentity: "acc-1", TrackerAccount: "jira"}
	got, err := newSources(t).For(context.Background(), w)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != application.SourceForge || got[0].IncludeIssues {
		t.Fatalf("%+v", got)
	}
	tracker := got[1]
	if tracker.Name != application.SourceTracker || tracker.Source.Service() != "Jira" || tracker.Source.Account() != "jira" {
		t.Fatalf("%+v", tracker)
	}
	if tracker.Rules.Identity != "acc-1" || tracker.Rules.Labels[domain.MeaningSpec] != "type::spec" || tracker.Rules.Marker != "m" {
		t.Fatalf("rules %+v", tracker.Rules)
	}
	w.Tracker = domain.Tracker{Kind: domain.TrackerGitHub}
	if got, err = newSources(t).For(context.Background(), w); err != nil || len(got) != 1 || !got[0].IncludeIssues {
		t.Fatalf("%v %+v", err, got)
	}
}

// A Jira site with no account does not take the forge down with it: the forge
// source is bound as ever, and the tracker's half is one whose every poll
// answers why it cannot be read, so the problem is the tracker's.
func TestAMissingJiraAccountLeavesTheForgeBoundAndFailsTheTrackerAlone(t *testing.T) {
	repo, _ := domain.ParseRepo("github.com/wwuser/x")
	w := application.Watch{Repo: repo, Identity: "wwuser", ForgeAccount: "gh", Marker: "m",
		Tracker: domain.Tracker{Kind: domain.TrackerJira, Site: "nobody.atlassian.net", Project: "KAN"}, TrackerAccount: "jira"}
	got, err := newSources(t).For(context.Background(), w)
	if err != nil {
		t.Fatalf("the forge was refused with the tracker: %v", err)
	}
	if len(got) != 2 || got[0].Name != application.SourceForge || got[0].Source.Service() != "GitHub" || got[0].IncludeIssues {
		t.Fatalf("the forge: %+v", got)
	}
	tracker := got[1]
	if tracker.Name != application.SourceTracker || tracker.Source.Service() != "Jira" {
		t.Fatalf("the tracker: %+v", tracker)
	}
	_, err = tracker.Source.Poll(context.Background(), application.PollInput{})
	var coded interface{ Code() string }
	if !errors.As(err, &coded) || coded.Code() != "no_account" || !strings.Contains(err.Error(), "nobody.atlassian.net") {
		t.Fatalf("the tracker's poll answers %v", err)
	}
	// With the account in place the tracker is the real source, not the stand-in.
	w.Tracker.Site = "wwsite.atlassian.net"
	got, err = newSources(t).For(context.Background(), w)
	if err != nil || len(got) != 2 || got[1].Source.Account() != "jira" {
		t.Fatalf("%v %+v", err, got)
	}
}
