// Package storetest is the contract every application.Store keeps, run
// over the in-memory fake and over state.db alike.
package storetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gperezmz/wakewire/internal/watch/application"
	"github.com/gperezmz/wakewire/internal/watch/domain"
)

var t0 = time.Date(2026, 10, 16, 9, 0, 0, 0, time.UTC)

// Run checks a store built by open.
func Run(t *testing.T, open func(t *testing.T) application.Store) {
	ctx := context.Background()
	repo, _ := domain.ParseRepo("github.com/o/x")
	newWatch := func(t *testing.T, s application.Store) application.Watch {
		w := application.Watch{Repo: repo, Identity: "jdoe", Tracker: domain.Tracker{Kind: domain.TrackerGitHub},
			Labels: domain.Labels{domain.MeaningSpec: "spec"}, Marker: "m", Interval: 45 * time.Second, CreatedAt: t0}
		must(t, s.Tx(ctx, func(tx application.Tx) error { return tx.SaveWatch(&w) }))
		if w.ID == 0 {
			t.Fatal("SaveWatch set no id")
		}
		return w
	}

	t.Run("a watch round-trips and is found by its key", func(t *testing.T) {
		s := open(t)
		w := newWatch(t, s)
		must(t, s.Tx(ctx, func(tx application.Tx) error {
			got, err := tx.WatchByKey("github.com/o/x", "jdoe")
			if err != nil || got == nil || got.ID != w.ID || got.Interval != 45*time.Second || got.Labels[domain.MeaningSpec] != "spec" ||
				got.Tracker.Kind != domain.TrackerGitHub || !got.CreatedAt.Equal(t0) {
				t.Fatalf("%+v %v", got, err)
			}
			other, err := tx.WatchByKey("github.com/o/x", "alice")
			if err != nil || other != nil {
				t.Fatalf("%+v %v", other, err)
			}
			return nil
		}))
	})

	t.Run("a failed transaction leaves nothing", func(t *testing.T) {
		s := open(t)
		boom := errors.New("boom")
		err := s.Tx(ctx, func(tx application.Tx) error {
			w := application.Watch{Repo: repo, Identity: "x", CreatedAt: t0}
			if err := tx.SaveWatch(&w); err != nil {
				return err
			}
			return boom
		})
		if !errors.Is(err, boom) {
			t.Fatal(err)
		}
		must(t, s.Tx(ctx, func(tx application.Tx) error {
			ws, err := tx.Watches()
			if len(ws) != 0 {
				t.Fatalf("%d watches survived a rollback", len(ws))
			}
			return err
		}))
	})

	t.Run("registrations are found by session and by the session they replaced", func(t *testing.T) {
		s := open(t)
		w := newWatch(t, s)
		r := application.Registration{WatchID: w.ID, Harness: "claude-code", Session: "s2", Process: application.Process{PID: 7, Start: "9"},
			RegisteredAt: t0, HeardAt: t0, Replaced: "s1"}
		must(t, s.Tx(ctx, func(tx application.Tx) error { return tx.SaveRegistration(r) }))
		must(t, s.Tx(ctx, func(tx application.Tx) error {
			a, _ := tx.RegistrationBySession("s2")
			b, _ := tx.RegistrationReplacing("s1")
			c, _ := tx.RegistrationBySession("s1")
			if a == nil || b == nil || c != nil || a.Process != r.Process || a.EndedAt != nil {
				t.Fatalf("%+v %+v %+v", a, b, c)
			}
			return nil
		}))
	})

	t.Run("adapters are found by their process", func(t *testing.T) {
		s := open(t)
		p := application.Process{PID: 7, Start: "9"}
		must(t, s.Tx(ctx, func(tx application.Tx) error {
			must(t, tx.SaveAdapter(application.Adapter{Session: "a", Process: p, HelloAt: t0, HeardAt: t0}))
			must(t, tx.SaveAdapter(application.Adapter{Session: "b", Process: p, HelloAt: t0, HeardAt: t0}))
			return tx.DeleteAdapter("a")
		}))
		must(t, s.Tx(ctx, func(tx application.Tx) error {
			as, err := tx.AdaptersOf(p)
			if len(as) != 1 || as[0].Session != "b" {
				t.Fatalf("%+v", as)
			}
			return err
		}))
	})

	t.Run("changes keep their status, body and batch", func(t *testing.T) {
		s := open(t)
		w := newWatch(t, s)
		var ids []int64
		must(t, s.Tx(ctx, func(tx application.Tx) error {
			for i, st := range []domain.Status{domain.StatusPending, domain.StatusIgnored, domain.StatusHeld} {
				c := domain.Change{Item: "#1", Kind: domain.KindComment, At: t0.Add(time.Duration(i) * time.Second), Status: st, Reason: "r"}.WithBody("hello")
				id, err := tx.AddChange(application.StoredChange{WatchID: w.ID, Source: application.SourceForge, Change: c, FoundAt: t0})
				if err != nil {
					return err
				}
				ids = append(ids, id)
			}
			b := &application.Batch{WatchID: w.ID, Changes: ids[:1], OfferedAt: t0}
			if err := tx.AddBatch(b); err != nil {
				return err
			}
			return tx.SetChangeStatus(ids[:1], domain.StatusDelivered, b.ID)
		}))
		must(t, s.Tx(ctx, func(tx application.Tx) error {
			got, err := tx.Changes(application.ChangeQuery{WatchID: w.ID, Status: []domain.Status{domain.StatusDelivered, domain.StatusHeld}})
			if err != nil || len(got) != 2 || got[0].Change.Status != domain.StatusDelivered || *got[0].Change.Body != "hello" ||
				got[0].Batch == 0 || got[1].Change.Reason != "r" {
				t.Fatalf("%+v %v", got, err)
			}
			b, err := tx.OpenBatch(w.ID)
			if err != nil || b == nil || len(b.Changes) != 1 || b.Changes[0] != ids[0] {
				t.Fatalf("%+v %v", b, err)
			}
			byID, _ := tx.Changes(application.ChangeQuery{IDs: []int64{}})
			if len(byID) != 0 {
				t.Fatal("an empty id list selected changes")
			}
			return nil
		}))
	})

	t.Run("a snapshot round-trips", func(t *testing.T) {
		s := open(t)
		w := newWatch(t, s)
		snap := domain.Snapshot{Item: domain.Item{Ref: "#1", Labels: []string{"spec"}, Comments: -1}, Seen: []string{"c1"}, Baseline: t0,
			Held: map[string]domain.HeldEdit{"title": {Before: "a", At: t0}}}
		must(t, s.Tx(ctx, func(tx application.Tx) error { return tx.SaveSnapshot(w.ID, application.SourceForge, snap) }))
		must(t, s.Tx(ctx, func(tx application.Tx) error {
			m, err := tx.Snapshots(w.ID, application.SourceForge)
			if got := m["#1"]; err != nil || got.Item.Comments != -1 || got.Held["title"].Before != "a" || len(got.Seen) != 1 {
				t.Fatalf("%+v %v", m, err)
			}
			if m, _ := tx.Snapshots(w.ID, application.SourceTracker); len(m) != 0 {
				t.Fatal("sources not kept apart")
			}
			return tx.DeleteSnapshot(w.ID, application.SourceForge, "#1")
		}))
	})

	t.Run("a lease refuses another process until free", func(t *testing.T) {
		s := open(t)
		w := newWatch(t, s)
		a, b := application.Process{PID: 1, Start: "1"}, application.Process{PID: 2, Start: "2"}
		take := func(p application.Process, free bool) bool {
			var ok bool
			must(t, s.Tx(ctx, func(tx application.Tx) error {
				var err error
				ok, err = tx.TakeLease(w.ID, p, t0.Add(time.Minute), func(application.Lease) bool { return free })
				return err
			}))
			return ok
		}
		if !take(a, false) || take(b, false) || !take(a, false) || !take(b, true) {
			t.Fatal("lease rules broken")
		}
		must(t, s.Tx(ctx, func(tx application.Tx) error { return tx.ReleaseLease(w.ID, b) }))
		if !take(a, false) {
			t.Fatal("a released lease was not free")
		}
	})

	t.Run("deleting a watch keeps the ledger of what was delivered", func(t *testing.T) {
		s := open(t)
		w := newWatch(t, s)
		must(t, s.Tx(ctx, func(tx application.Tx) error {
			for _, st := range []domain.Status{domain.StatusDelivered, domain.StatusPending} {
				if _, err := tx.AddChange(application.StoredChange{WatchID: w.ID, Source: application.SourceForge,
					Change: domain.Change{Item: "#1", Kind: domain.KindClosed, At: t0, Status: st}, FoundAt: t0}); err != nil {
					return err
				}
			}
			return tx.DeleteWatch(w.ID)
		}))
		must(t, s.Tx(ctx, func(tx application.Tx) error {
			cs, err := tx.Changes(application.ChangeQuery{WatchID: w.ID})
			if len(cs) != 1 || cs[0].Change.Status != domain.StatusDelivered {
				t.Fatalf("%+v", cs)
			}
			return err
		}))
	})

	t.Run("prune removes what aged out and records when", func(t *testing.T) {
		s := open(t)
		w := newWatch(t, s)
		old := t0.Add(-40 * 24 * time.Hour)
		must(t, s.Tx(ctx, func(tx application.Tx) error {
			_, err := tx.AddChange(application.StoredChange{WatchID: w.ID, Source: application.SourceForge,
				Change: domain.Change{Item: "#1", Kind: domain.KindClosed, At: old, Status: domain.StatusIgnored}, FoundAt: old})
			if err != nil {
				return err
			}
			return tx.SaveRegistration(application.Registration{WatchID: w.ID, Harness: "claude-code", Session: "s",
				Process: application.Process{PID: 1, Start: "1"}, RegisteredAt: old, HeardAt: old})
		}))
		plan := application.PrunePlan{Now: t0, Before: t0.Add(-domain.KeepFor), Dormant: func(application.Registration) bool { return true }}
		must(t, s.Tx(ctx, func(tx application.Tx) error {
			c, err := tx.Prune(plan, true)
			if c.Changes != 1 || c.Registrations != 1 {
				t.Fatalf("dry run %+v", c)
			}
			return err
		}))
		must(t, s.Tx(ctx, func(tx application.Tx) error {
			_, err := tx.Prune(plan, false)
			return err
		}))
		must(t, s.Tx(ctx, func(tx application.Tx) error {
			at, err := tx.PrunedAt()
			ws, _ := tx.Watches()
			if !at.Equal(t0) || len(ws) != 0 {
				t.Fatalf("%v %d", at, len(ws))
			}
			return err
		}))
	})
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
