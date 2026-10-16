package sources_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/gperezmz/wakewire/internal/adapters/home"
	"github.com/gperezmz/wakewire/internal/adapters/httpread"
	"github.com/gperezmz/wakewire/internal/watch/adapters/accounts"
	"github.com/gperezmz/wakewire/internal/watch/adapters/sources"
	"github.com/gperezmz/wakewire/internal/watch/application"
	"github.com/gperezmz/wakewire/internal/watch/domain"
	"github.com/gperezmz/wakewire/internal/wiring" //nolint:depguard // test only: the account service is built by the composition root
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
