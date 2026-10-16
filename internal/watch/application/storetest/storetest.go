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

	t.Run("a deleted watch's id is never given to another watch", func(t *testing.T) {
		s := open(t)
		first := newWatch(t, s)
		must(t, s.Tx(ctx, func(tx application.Tx) error {
			_, err := tx.AddChange(application.StoredChange{WatchID: first.ID, Source: application.SourceForge,
				Change: domain.Change{Item: "#1", Kind: domain.KindClosed, At: t0, Status: domain.StatusDelivered}, FoundAt: t0})
			if err != nil {
				return err
			}
			return tx.DeleteWatch(first.ID)
		}))
		second := application.Watch{Repo: repo, Identity: "alice", CreatedAt: t0}
		must(t, s.Tx(ctx, func(tx application.Tx) error { return tx.SaveWatch(&second) }))
		if second.ID == first.ID {
			t.Fatalf("watch id %d was given out twice", second.ID)
		}
		must(t, s.Tx(ctx, func(tx application.Tx) error {
			cs, err := tx.Changes(application.ChangeQuery{WatchID: second.ID})
			if len(cs) != 0 {
				t.Fatalf("a new watch inherited %d change(s) of a deleted one", len(cs))
			}
			return err
		}))
	})

	t.Run("a pruned batch's id is never given to another batch", func(t *testing.T) {
		s := open(t)
		w := newWatch(t, s)
		old := t0.Add(-40 * 24 * time.Hour)
		b := &application.Batch{WatchID: w.ID, OfferedAt: old, AckedAt: &old}
		must(t, s.Tx(ctx, func(tx application.Tx) error { return tx.AddBatch(b) }))
		plan := application.PrunePlan{Now: t0, Before: t0.Add(-domain.KeepFor), Dormant: func(application.Registration) bool { return false }}
		must(t, s.Tx(ctx, func(tx application.Tx) error { _, err := tx.Prune(plan, false); return err }))
		next := &application.Batch{WatchID: w.ID, OfferedAt: t0}
		must(t, s.Tx(ctx, func(tx application.Tx) error { return tx.AddBatch(next) }))
		if next.ID == b.ID {
			t.Fatalf("batch id %d was given out twice", next.ID)
		}
	})

	t.Run("a watch keeps its pause, its poll time and its settings through a save", func(t *testing.T) {
		s := open(t)
		w := newWatch(t, s)
		since, until, polled := t0.Add(time.Minute), t0.Add(time.Hour), t0.Add(2*time.Minute)
		w.PausedSince, w.PausedUntil, w.PolledAt = &since, &until, polled
		w.ForgeAccount, w.TrackerAccount, w.TrackerIdentity = "gh", "jira-acct", "acct-id"
		w.Interval = 90 * time.Second
		must(t, s.Tx(ctx, func(tx application.Tx) error { return tx.SaveWatch(&w) }))
		must(t, s.Tx(ctx, func(tx application.Tx) error {
			got, err := tx.Watch(w.ID)
			if err != nil || got == nil || got.PausedSince == nil || !got.PausedSince.Equal(since) || got.PausedUntil == nil ||
				!got.PausedUntil.Equal(until) || !got.PolledAt.Equal(polled) || got.ForgeAccount != "gh" ||
				got.TrackerAccount != "jira-acct" || got.TrackerIdentity != "acct-id" || got.Interval != 90*time.Second {
				t.Fatalf("%+v %v", got, err)
			}
			got.PausedSince, got.PausedUntil = nil, nil
			return tx.SaveWatch(got)
		}))
		must(t, s.Tx(ctx, func(tx application.Tx) error {
			got, err := tx.Watch(w.ID)
			if err != nil || got.PausedSince != nil || got.PausedUntil != nil {
				t.Fatalf("a cleared pause came back: %+v %v", got, err)
			}
			all, _ := tx.Watches()
			if len(all) != 1 {
				t.Fatalf("a save of a known watch made %d watches", len(all))
			}
			return nil
		}))
	})

	t.Run("a registration keeps when it ended and why", func(t *testing.T) {
		s := open(t)
		w := newWatch(t, s)
		ended := t0.Add(5 * time.Minute)
		r := application.Registration{WatchID: w.ID, Harness: "claude-code", Session: "s1", Process: application.Process{PID: 7, Start: "9"},
			RegisteredAt: t0, HeardAt: t0.Add(time.Minute)}
		must(t, s.Tx(ctx, func(tx application.Tx) error { return tx.SaveRegistration(r) }))
		r.EndedAt, r.EndReason, r.Process = &ended, "prompt_input_exit", application.Process{PID: 8, Start: "10"}
		must(t, s.Tx(ctx, func(tx application.Tx) error { return tx.SaveRegistration(r) }))
		must(t, s.Tx(ctx, func(tx application.Tx) error {
			got, err := tx.Registration(w.ID)
			if err != nil || got == nil || got.EndedAt == nil || !got.EndedAt.Equal(ended) || got.EndReason != "prompt_input_exit" ||
				got.Process != r.Process || !got.HeardAt.Equal(r.HeardAt) || !got.RegisteredAt.Equal(t0) || got.Session != "s1" {
				t.Fatalf("%+v %v", got, err)
			}
			return nil
		}))
	})

	t.Run("a batch keeps when it was acknowledged, and the newest open batch is the one offered", func(t *testing.T) {
		s := open(t)
		w := newWatch(t, s)
		var ids []int64
		must(t, s.Tx(ctx, func(tx application.Tx) error {
			for i := range 3 {
				c := domain.Change{Item: "#1", Kind: domain.KindComment, At: t0, Status: domain.StatusPending}
				id, err := tx.AddChange(application.StoredChange{WatchID: w.ID, Source: application.SourceForge, Change: c, FoundAt: t0})
				if err != nil {
					return err
				}
				b := &application.Batch{WatchID: w.ID, Changes: []int64{id}, OfferedAt: t0.Add(time.Duration(i) * time.Minute)}
				if err := tx.AddBatch(b); err != nil {
					return err
				}
				ids = append(ids, b.ID)
			}
			return nil
		}))
		acked := t0.Add(10 * time.Minute)
		must(t, s.Tx(ctx, func(tx application.Tx) error {
			b, err := tx.Batch(ids[2])
			if err != nil || b == nil || b.AckedAt != nil || len(b.Changes) != 1 {
				t.Fatalf("%+v %v", b, err)
			}
			open, _ := tx.OpenBatch(w.ID)
			if open == nil || open.ID != ids[2] {
				t.Fatalf("the open batch is not the newest: %+v", open)
			}
			b.AckedAt = &acked
			return tx.SaveBatch(*b)
		}))
		must(t, s.Tx(ctx, func(tx application.Tx) error {
			b, err := tx.Batch(ids[2])
			if err != nil || b.AckedAt == nil || !b.AckedAt.Equal(acked) {
				t.Fatalf("the acknowledgement was not kept: %+v %v", b, err)
			}
			open, _ := tx.OpenBatch(w.ID)
			if open == nil || open.ID != ids[1] {
				t.Fatalf("an acknowledged batch is still open, or the wrong one is: %+v", open)
			}
			none, err := tx.OpenBatch(w.ID + 100)
			if none != nil || err != nil {
				t.Fatalf("%+v %v", none, err)
			}
			return nil
		}))
	})

	t.Run("a source keeps its state between polls", func(t *testing.T) {
		s := open(t)
		w := newWatch(t, s)
		must(t, s.Tx(ctx, func(tx application.Tx) error {
			got, err := tx.SourceState(w.ID, application.SourceForge)
			if err != nil || got.Failures != 0 || got.Data != nil || !got.OKAt.IsZero() || !got.SkipUntil.IsZero() {
				t.Fatalf("a source never polled has state: %+v %v", got, err)
			}
			return nil
		}))
		st := application.SourceState{WatchID: w.ID, Source: application.SourceForge, Data: []byte(`{"etag":"x"}`), Failures: 2,
			Rate:      application.RateInfo{Remaining: 10, Limit: 100, ResetAt: t0.Add(time.Hour), NearLimit: true},
			SkipUntil: t0.Add(time.Hour), OKAt: t0.Add(time.Minute)}
		must(t, s.Tx(ctx, func(tx application.Tx) error { return tx.SaveSourceState(st) }))
		st.Failures, st.Data = 0, []byte(`{"etag":"y"}`)
		st.SkipUntil, st.OKAt = t0.Add(2*time.Hour), t0.Add(3*time.Minute) // every column is updated, not only the first save's
		must(t, s.Tx(ctx, func(tx application.Tx) error { return tx.SaveSourceState(st) }))
		must(t, s.Tx(ctx, func(tx application.Tx) error {
			got, err := tx.SourceState(w.ID, application.SourceForge)
			if err != nil || string(got.Data) != `{"etag":"y"}` || got.Failures != 0 || got.Rate.Remaining != 10 || got.Rate.Limit != 100 ||
				!got.Rate.ResetAt.Equal(st.Rate.ResetAt) || !got.Rate.NearLimit || !got.SkipUntil.Equal(st.SkipUntil) || !got.OKAt.Equal(st.OKAt) {
				t.Fatalf("%+v %v", got, err)
			}
			other, _ := tx.SourceState(w.ID, application.SourceTracker)
			if other.Data != nil || other.Failures != 0 {
				t.Fatalf("sources not kept apart: %+v", other)
			}
			return nil
		}))
	})

	t.Run("a problem keeps its fix, its delivery and its clearing", func(t *testing.T) {
		s := open(t)
		w := newWatch(t, s)
		p := &application.Problem{WatchID: w.ID, Source: application.SourceTracker, Code: "token_refused", Text: "refused", Hint: "replace it", Since: t0}
		must(t, s.Tx(ctx, func(tx application.Tx) error { return tx.AddProblem(p) }))
		if p.ID == 0 {
			t.Fatal("AddProblem set no id")
		}
		delivered, cleared := t0.Add(time.Minute), t0.Add(2*time.Minute)
		must(t, s.Tx(ctx, func(tx application.Tx) error {
			open, err := tx.OpenProblems(w.ID)
			if err != nil || len(open) != 1 || open[0].Hint != "replace it" || open[0].Source != application.SourceTracker ||
				open[0].DeliveredAt != nil || !open[0].Since.Equal(t0) {
				t.Fatalf("%+v %v", open, err)
			}
			open[0].DeliveredAt = &delivered
			return tx.SaveProblem(open[0])
		}))
		must(t, s.Tx(ctx, func(tx application.Tx) error {
			open, _ := tx.OpenProblems(w.ID)
			if len(open) != 1 || open[0].DeliveredAt == nil || !open[0].DeliveredAt.Equal(delivered) {
				t.Fatalf("the delivery was not kept: %+v", open)
			}
			open[0].ClearedAt = &cleared
			return tx.SaveProblem(open[0])
		}))
		must(t, s.Tx(ctx, func(tx application.Tx) error {
			open, err := tx.OpenProblems(w.ID)
			if len(open) != 0 {
				t.Fatalf("a cleared problem is still open: %+v", open)
			}
			return err
		}))
	})

	t.Run("the lease the store hands to free is the one it holds", func(t *testing.T) {
		s := open(t)
		w := newWatch(t, s)
		a, b := application.Process{PID: 1, Start: "1"}, application.Process{PID: 2, Start: "2"}
		until := t0.Add(90 * time.Second)
		must(t, s.Tx(ctx, func(tx application.Tx) error {
			ok, err := tx.TakeLease(w.ID, a, until, func(application.Lease) bool { t.Fatal("free asked of an empty lease"); return false })
			if !ok || err != nil {
				t.Fatal(ok, err)
			}
			return nil
		}))
		var seen application.Lease
		must(t, s.Tx(ctx, func(tx application.Tx) error {
			ok, err := tx.TakeLease(w.ID, b, until.Add(time.Minute), func(l application.Lease) bool { seen = l; return false })
			if ok || err != nil {
				t.Fatal(ok, err)
			}
			return nil
		}))
		if seen.WatchID != w.ID || seen.Holder != a || !seen.ExpiresAt.Equal(until) {
			t.Fatalf("free was shown %+v", seen)
		}
		must(t, s.Tx(ctx, func(tx application.Tx) error {
			ok, err := tx.TakeLease(w.ID, b, until.Add(time.Minute), func(application.Lease) bool { return true })
			if !ok || err != nil {
				t.Fatal(ok, err)
			}
			return nil
		}))
		must(t, s.Tx(ctx, func(tx application.Tx) error {
			// a releases a lease b holds now: nothing happens
			if err := tx.ReleaseLease(w.ID, a); err != nil {
				return err
			}
			ok, _ := tx.TakeLease(w.ID, a, until, func(application.Lease) bool { return false })
			if ok {
				t.Fatal("a release by a process that did not hold the lease freed it")
			}
			return nil
		}))
	})

	t.Run("changes are found by item, by batch and by when they were found", func(t *testing.T) {
		s := open(t)
		w := newWatch(t, s)
		must(t, s.Tx(ctx, func(tx application.Tx) error {
			for i, item := range []string{"#1", "#2", "#2"} {
				c := domain.Change{Item: item, Kind: domain.KindComment, At: t0, Status: domain.StatusPending}
				if _, err := tx.AddChange(application.StoredChange{WatchID: w.ID, Source: application.SourceForge, Change: c,
					FoundAt: t0.Add(time.Duration(i) * time.Hour)}); err != nil {
					return err
				}
			}
			return nil
		}))
		must(t, s.Tx(ctx, func(tx application.Tx) error {
			byItem, _ := tx.Changes(application.ChangeQuery{WatchID: w.ID, Item: "#2"})
			recent, _ := tx.Changes(application.ChangeQuery{WatchID: w.ID, Since: t0.Add(30 * time.Minute)})
			if len(byItem) != 2 || len(recent) != 2 {
				t.Fatalf("by item %d, since %d", len(byItem), len(recent))
			}
			ids := []int64{byItem[0].ID}
			if err := tx.SetChangeStatus(ids, domain.StatusPending, 41); err != nil {
				return err
			}
			if err := tx.SetChangeStatus(ids, domain.StatusDelivered, 0); err != nil { // batch 0 leaves the batch as it was
				return err
			}
			inBatch, _ := tx.Changes(application.ChangeQuery{WatchID: w.ID, Batch: 41})
			if len(inBatch) != 1 || inBatch[0].Change.Status != domain.StatusDelivered {
				t.Fatalf("%+v", inBatch)
			}
			return nil
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
