package sqlitestore_test

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gperezmz/wakewire/internal/watch/adapters/sqlitestore"
	"github.com/gperezmz/wakewire/internal/watch/application"
	"github.com/gperezmz/wakewire/internal/watch/application/fakes"
	"github.com/gperezmz/wakewire/internal/watch/domain"
)

// These tests kill a real process with SIGKILL in the middle of a poll,
// once while it waits on the service and once inside the transaction that
// stores what it found, and show that the next poll still finds the change.

const agents = "## Forge and tracker\n\nTracker: github-issues\n\n| Meaning | Label |\n|---|---|\n| spec | spec |\n| ready | ready |\n| in progress | in-progress |\n| needs input | needs-input |\n"

var start = time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)

func spec(labels ...string) domain.Item {
	return domain.Item{Ref: "#12", Kind: domain.ItemIssue, URL: "u", State: "open", Labels: append([]string{"spec"}, labels...),
		Assignees: []string{"jdoe"}, Author: "alice"}
}

func service(store application.Store, src *fakes.Source, clock *fakes.Clock) *application.Service {
	repo, _ := domain.ParseRepo("github.com/o/x")
	return application.New(store, &fakes.Sources{Source: src},
		&fakes.Accounts{Forge: application.AccountIdentity{Account: "gh", Service: "GitHub", Identity: "jdoe"}},
		&fakes.Repos{Repo: repo, Files: map[string]string{"AGENTS.md": agents}},
		&fakes.Processes{Running: map[int]string{100: "s100"}}, clock, application.Settings{Template: domain.DefaultTemplate()})
}

// pausingStore stops forever inside the transaction after the source
// answered, once its writes are made and before they commit.
type pausingStore struct {
	inner    application.Store
	answered *atomic.Bool
}

func (p pausingStore) Tx(ctx context.Context, fn func(application.Tx) error) error {
	return p.inner.Tx(ctx, func(tx application.Tx) error {
		if err := fn(tx); err != nil {
			return err
		}
		if p.answered.Load() {
			fmt.Println("READY")
			select {}
		}
		return nil
	})
}

func TestKillHelper(t *testing.T) {
	db, stage := os.Getenv("WAKEWIRE_KILL_DB"), os.Getenv("WAKEWIRE_KILL_STAGE")
	if db == "" {
		t.Skip("run by TestKilledMidPollLosesNothing")
	}
	store, err := sqlitestore.Open(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	clock := &fakes.Clock{T: start.Add(3 * time.Minute)}
	answered := &atomic.Bool{}
	src := &fakes.Source{Name: "GitHub"}
	src.Script = []func(application.PollInput) (application.PollOutput, error){func(application.PollInput) (application.PollOutput, error) {
		if stage == "source" {
			fmt.Println("READY")
			select {}
		}
		answered.Store(true)
		return application.PollOutput{Observations: []domain.Observation{{Item: spec("ready"), At: clock.Now()}}, At: clock.Now()}, nil
	}}
	_, err = service(pausingStore{store, answered}, src, clock).Next(context.Background(), "s1", 100)
	t.Fatalf("the poll finished: %v", err)
}

func TestKilledMidPollLosesNothing(t *testing.T) {
	for _, stage := range []string{"source", "commit"} {
		t.Run(stage, func(t *testing.T) {
			db := filepath.Join(t.TempDir(), "state.db")
			store, err := sqlitestore.Open(context.Background(), db)
			if err != nil {
				t.Fatal(err)
			}
			clock := &fakes.Clock{T: start}
			src := &fakes.Source{Name: "GitHub"}
			svc := service(store, src, clock)
			if _, err := svc.Register(context.Background(), application.RegisterInput{Session: "s1", Process: 100}); err != nil {
				t.Fatal(err)
			}
			src.Script = append(src.Script, func(application.PollInput) (application.PollOutput, error) {
				return application.PollOutput{Observations: []domain.Observation{{Item: spec(), At: start}}, At: start}, nil
			})
			if _, err := svc.Next(context.Background(), "s1", 100); err != nil {
				t.Fatal(err)
			}
			_ = store.Close()

			cmd := exec.Command(os.Args[0], "-test.run=^TestKillHelper$", "-test.v") //nolint:gosec // re-runs this test binary
			cmd.Env = append(os.Environ(), "WAKEWIRE_KILL_DB="+db, "WAKEWIRE_KILL_STAGE="+stage)
			out, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			ready := make(chan bool)
			go func() {
				sc := bufio.NewScanner(out)
				for sc.Scan() {
					if strings.TrimSpace(sc.Text()) == "READY" {
						ready <- true
						return
					}
				}
				ready <- false
			}()
			select {
			case ok := <-ready:
				if !ok {
					t.Fatal("the helper ended without reaching the point to kill it")
				}
			case <-time.After(30 * time.Second):
				t.Fatal("the helper never reached the point to kill it")
			}
			if err := cmd.Process.Kill(); err != nil { // SIGKILL
				t.Fatal(err)
			}
			_ = cmd.Wait()

			store, err = sqlitestore.Open(context.Background(), db)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = store.Close() }()
			clock.T = start.Add(6 * time.Minute)
			svc = service(store, src, clock)
			led, err := svc.Ledger(context.Background(), application.Selector{Repo: "github.com/o/x"}, 0, "")
			if err != nil || len(led.Changes) != 0 {
				t.Fatalf("the killed poll left a trace: %+v %v", led, err)
			}
			src.Script = append(src.Script, func(application.PollInput) (application.PollOutput, error) {
				return application.PollOutput{Observations: []domain.Observation{{Item: spec("ready"), At: clock.Now()}}, At: clock.Now()}, nil
			})
			n, err := svc.Next(context.Background(), "s1", 100)
			if err != nil || n.Batch == nil || !strings.Contains(n.Batch.Wake, `label "ready" added`) {
				t.Fatalf("the change was lost: %+v %v", n, err)
			}
		})
	}
}
