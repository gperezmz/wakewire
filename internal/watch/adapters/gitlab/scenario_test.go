package gitlab_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/gperezmz/wakewire/internal/adapters/contract"
	"github.com/gperezmz/wakewire/internal/adapters/httpread"
	"github.com/gperezmz/wakewire/internal/watch/adapters/gitlab"
	"github.com/gperezmz/wakewire/internal/watch/application"
	"github.com/gperezmz/wakewire/internal/watch/application/fakes"
	"github.com/gperezmz/wakewire/internal/watch/domain"
)

// The scenario is a run of polls against a sandbox project, with changes
// made on GitLab between them. record_test.go runs it against a real
// instance and writes the contract and the wakes it saw; this file replays
// the contract and expects the same wakes.

const (
	contractFile = "testdata/contract.json"
	expectFile   = "testdata/expect.json"
	sandbox      = "gitlab.example.com/wwuser/wakewire-sandbox"
)

// step is one poll and the wake it gave, if any.
type step struct {
	Name string `json:"name"`
	Wake string `json:"wake"`
}

const agents = "## Forge and tracker\n\nTracker: gitlab-issues\n\n| Meaning | Label |\n| --- | --- |\n| spec | `type::spec` |\n| ready | `ready-for-dev` |\n| in progress | `in-progress` |\n| needs input | `needs-input` |\n"

// runner drives polls through the real use cases over the fake store.
type runner struct {
	t     *testing.T
	svc   *application.Service
	clock *fakes.Clock
}

func newRunner(t *testing.T, repo, identity, origin string, transport http.RoundTripper, token string) *runner {
	r, _ := domain.ParseRepo(repo)
	clock := &fakes.Clock{T: time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)}
	src := &gitlab.Source{HTTP: httpread.New("wakewire", transport), Base: origin, Token: token, Repo: r, Name: "work"}
	svc := application.New(fakes.NewStore(), staticSources{src}, &fakes.Accounts{
		Forge: application.AccountIdentity{Account: "work", Service: "GitLab", Identity: identity},
	}, &fakes.Repos{Repo: r, Files: map[string]string{"AGENTS.md": agents}},
		&fakes.Processes{Running: map[int]string{1: "1"}}, clock, application.Settings{Template: domain.DefaultTemplate(), Interval: 30 * time.Second})
	if _, err := svc.Register(context.Background(), application.RegisterInput{Session: "s", Process: 1}); err != nil {
		t.Fatal(err)
	}
	return &runner{t: t, svc: svc, clock: clock}
}

type staticSources struct{ src *gitlab.Source }

func (s staticSources) For(_ context.Context, w application.Watch) ([]application.BoundSource, error) {
	return []application.BoundSource{{Name: application.SourceForge, Source: s.src, IncludeIssues: true,
		Rules: domain.Rules{Identity: w.Identity, Labels: w.Labels, Marker: w.Marker}}}, nil
}

// poll runs one tick and acknowledges what it offered, answering the wake.
func (r *runner) poll() string {
	r.t.Helper()
	r.clock.Advance(31 * time.Second)
	n, err := r.svc.Next(context.Background(), "s", 1)
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
	r := newRunner(t, sandbox, "wwuser", srv.URL, nil, "replayed")
	for _, s := range want {
		if got := r.poll(); got != s.Wake {
			t.Errorf("%s:\ngot  %q\nwant %q", s.Name, got, s.Wake)
		}
	}
}
