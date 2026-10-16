package jira_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/gperezmz/wakewire/internal/adapters/contract"
	"github.com/gperezmz/wakewire/internal/adapters/httpread"
	"github.com/gperezmz/wakewire/internal/watch/adapters/jira"
	"github.com/gperezmz/wakewire/internal/watch/application"
	"github.com/gperezmz/wakewire/internal/watch/application/fakes"
	"github.com/gperezmz/wakewire/internal/watch/domain"
)

// The scenario is a run of polls against the KAN project of a Jira Cloud
// sandbox, with changes made on Jira between them. record_test.go runs it
// against Jira and writes the contract and the wakes it saw; this file
// replays the contract and expects the same wakes.

const (
	contractFile = "testdata/contract.json"
	expectFile   = "testdata/expect.json"
	// What the recording is scrubbed to: the sandbox's site and the account.
	site     = "wwsite.atlassian.net"
	account  = "wwaccount"
	person   = "Wakewire User"
	forgeRep = "github.com/wwuser/wakewire-sandbox"
)

// step is one poll and the wake it gave, if any.
type step struct {
	Name string `json:"name"`
	Wake string `json:"wake"`
}

const agents = "## Forge and tracker\n\nTracker: jira:wwsite.atlassian.net/KAN\n\n| Meaning | Label |\n| --- | --- |\n| spec | `type::spec` |\n| ready | `ready-for-dev` |\n| in progress | `in-progress` |\n| needs input | `needs-input` |\n"

// runner drives polls through the real use cases over the fake store.
type runner struct {
	t     *testing.T
	svc   *application.Service
	clock *fakes.Clock
}

// newRunner watches the Jira project as accountID, reading through base
// and transport (nil for the default).
func newRunner(t *testing.T, siteName, accountID, base string, transport http.RoundTripper, email, token string) *runner {
	r, _ := domain.ParseRepo(forgeRep)
	// Long before any recording, so every recorded change is after the first poll.
	clock := &fakes.Clock{T: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}
	src := &jira.Source{HTTP: httpread.New("wakewire", transport), Base: base, Site: siteName, Project: "KAN",
		Email: email, Token: token, Name: "sandbox-jira"}
	svc := application.New(fakes.NewStore(), staticSources{src: src, identity: accountID}, &fakes.Accounts{
		Forge: application.AccountIdentity{Account: "personal", Service: "GitHub", Identity: "wwuser"},
		Jira:  application.AccountIdentity{Account: "sandbox-jira", Service: "Jira", Identity: accountID, Display: person},
	}, &fakes.Repos{Repo: r, Files: map[string]string{"AGENTS.md": agentsFor(siteName)}},
		&fakes.Processes{Running: map[int]string{1: "1"}}, clock, application.Settings{Template: domain.DefaultTemplate(), Interval: 30 * time.Second})
	if _, err := svc.Register(context.Background(), application.RegisterInput{Session: "s", Process: 1}); err != nil {
		t.Fatal(err)
	}
	return &runner{t: t, svc: svc, clock: clock}
}

func agentsFor(siteName string) string {
	if siteName == site {
		return agents
	}
	return "## Forge and tracker\n\nTracker: jira:" + siteName + "/KAN\n\n| Meaning | Label |\n| --- | --- |\n| spec | `type::spec` |\n| ready | `ready-for-dev` |\n| in progress | `in-progress` |\n| needs input | `needs-input` |\n"
}

// staticSources gives the watch its Jira source and no forge source.
type staticSources struct {
	src      *jira.Source
	identity string
}

func (s staticSources) For(_ context.Context, w application.Watch) ([]application.BoundSource, error) {
	return []application.BoundSource{{Name: application.SourceTracker, Source: s.src,
		Rules: domain.Rules{Identity: s.identity, Labels: w.Labels, Marker: w.Marker}}}, nil
}

// poll runs one tick and acknowledges what it offered, answering the wake.
func (r *runner) poll() string {
	r.t.Helper()
	r.clock.Advance(31 * time.Second)
	n, err := r.svc.Next(context.Background(), "s", 1, domain.ReaderTool)
	if err != nil {
		r.t.Fatal(err)
	}
	if !n.Polled {
		r.t.Fatal("the tick did not poll")
	}
	if n.Batch == nil {
		return ""
	}
	if _, err := r.svc.Ack(context.Background(), n.Batch.ID); err != nil {
		r.t.Fatal(err)
	}
	return n.Batch.Wake
}

func TestRecordedScenarioGivesTheSameWakes(t *testing.T) {
	time.Local = time.UTC
	c, err := contract.Load(contractFile)
	if err != nil {
		t.Skipf("no recording yet: %v", err)
	}
	var want []step
	b, err := os.ReadFile(expectFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &want); err != nil {
		t.Fatal(err)
	}
	srv := contract.Serve(t, c)
	r := newRunner(t, site, account, srv.URL, nil, "ww@example.com", "replayed")
	// WAKEWIRE_UPDATE_EXPECT=1 writes the wakes the replay gives as the
	// expected ones, for when the wake's text changes but not what was read.
	update := os.Getenv("WAKEWIRE_UPDATE_EXPECT") == "1"
	for i, s := range want {
		got := r.poll()
		if update {
			want[i].Wake = got
			continue
		}
		if got != s.Wake {
			t.Errorf("%s:\ngot  %q\nwant %q", s.Name, got, s.Wake)
		}
	}
	if update {
		b, err := json.MarshalIndent(want, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(expectFile, append(b, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
