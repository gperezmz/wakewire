package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gperezmz/wakewire/internal/watch/domain"
)

// HelloResult answers whether the session holds a watch.
type HelloResult struct {
	Protocol int        `json:"protocol"`
	Holds    bool       `json:"holds"`
	Watch    string     `json:"watch,omitempty"`
	Status   *LineState `json:"status,omitempty"`
}

// LineState is what an adapter's status line shows.
type LineState struct {
	Watch       string     `json:"watch"`
	PolledAt    *time.Time `json:"polledAt"`
	PausedSince *time.Time `json:"pausedSince"`
	Held        int        `json:"held"`
	Pending     int        `json:"pending"`
	Problems    []Issue    `json:"problems"`
	// LastChangeAt is when the newest change that was not ignored was found,
	// within the last day; null when there is none.
	LastChangeAt *time.Time `json:"lastChangeAt"`
}

// NextResult is the tick's answer.
type NextResult struct {
	State   string     `json:"state"` // armed, idle, taken-over
	By      string     `json:"by,omitempty"`
	Polled  bool       `json:"polled"`
	Batch   *Offer     `json:"batch"`
	Status  *LineState `json:"status,omitempty"`
	Warning string     `json:"warning,omitempty"`
}

// Offer is a batch offered for delivery.
type Offer struct {
	ID    int64  `json:"id"`
	Wake  string `json:"wake"`
	Count int    `json:"count"`
}

// Hello records that an adapter is loaded for a session.
func (s *Service) Hello(ctx context.Context, session string, proc, protocol int) (HelloResult, error) {
	res := HelloResult{Protocol: domain.Protocol}
	if protocol != 0 && protocol != domain.Protocol {
		return res, domain.Refuse("protocol_mismatch",
			fmt.Sprintf("needs update: the binary speaks protocol %d, the adapter %d", domain.Protocol, protocol),
			"install the binary and the mod from the same release")
	}
	start, _, err := s.processes.StartOf(proc)
	if err != nil {
		return res, err
	}
	now := s.clock.Now()
	err = s.store.Tx(ctx, func(tx Tx) error {
		if err := tx.SaveAdapter(Adapter{Session: session, Process: Process{proc, start}, HelloAt: now, HeardAt: now, Protocol: protocol}); err != nil {
			return err
		}
		if err := s.revive(tx, session, Process{proc, start}, now); err != nil {
			return err
		}
		w, _, err := s.held(tx, session)
		if err != nil || w == nil {
			return err
		}
		res.Holds, res.Watch = true, w.Repo.String()
		res.Status, err = s.lineState(tx, *w)
		return err
	})
	return res, err
}

// revive gives a session its registration back when it comes back under
// the same id, as `claude --resume` brings it, in a new process: the
// registration had gone dormant only because that session or its process
// went away, and no other session has taken the watch over since.
func (s *Service) revive(tx Tx, session string, p Process, now time.Time) error {
	r, err := tx.RegistrationBySession(session)
	if err != nil || r == nil {
		return err
	}
	cur, err := tx.Registration(r.WatchID)
	if err != nil || cur == nil || cur.Session != session {
		return err
	}
	if cur.EndedAt == nil {
		if cur.Process == p {
			return nil
		}
		start, running, err := s.processes.StartOf(cur.Process.PID)
		if err != nil {
			return err
		}
		if running && start == cur.Process.Start {
			return nil // the registering process still runs: this is another session's process
		}
	} else if !domain.Resumable(cur.EndReason) {
		return nil // ended on purpose: cleared, or registered for another watch
	}
	cur.EndedAt, cur.EndReason, cur.Process, cur.HeardAt = nil, "", p, now
	return tx.SaveRegistration(*cur)
}

// held answers the watch a session holds, if its registration is current.
func (s *Service) held(tx Tx, session string) (*Watch, *Registration, error) {
	r, err := tx.RegistrationBySession(session)
	if err != nil || r == nil || r.EndedAt != nil {
		return nil, nil, err
	}
	w, err := tx.Watch(r.WatchID)
	if err != nil || w == nil {
		return nil, nil, err
	}
	cur, err := tx.Registration(w.ID)
	if err != nil || cur == nil || cur.Session != session {
		return nil, nil, err
	}
	return w, cur, nil
}

// Next is the tick: it records the adapter as heard from, polls if due
// within the poll budget, and offers the pending batch if any.
func (s *Service) Next(ctx context.Context, session string, proc int) (NextResult, error) {
	res := NextResult{State: "idle"}
	start, _, err := s.processes.StartOf(proc)
	if err != nil {
		return res, err
	}
	self := Process{proc, start}
	now := s.clock.Now()
	var w *Watch
	err = s.store.Tx(ctx, func(tx Tx) error {
		a, err := tx.Adapter(session)
		if err != nil {
			return err
		}
		if a == nil || a.Process != self {
			a = &Adapter{Session: session, Process: self, HelloAt: now, Protocol: domain.Protocol}
		}
		a.HeardAt = now
		if err := tx.SaveAdapter(*a); err != nil {
			return err
		}
		var reg *Registration
		if w, reg, err = s.held(tx, session); err != nil {
			return err
		}
		if w == nil {
			if by, err := tx.RegistrationReplacing(session); err != nil {
				return err
			} else if by != nil {
				res.State, res.By = "taken-over", by.Harness+":"+by.Session
			}
			return nil
		}
		reg.HeardAt = now
		res.State = "armed"
		return tx.SaveRegistration(*reg)
	})
	if err != nil || w == nil {
		return res, err
	}
	if err := s.pruneIfDue(ctx); err != nil {
		return res, err
	}
	if err := s.endPauseIfDue(ctx, w.ID); err != nil {
		return res, err
	}
	if !w.PolledAt.IsZero() && now.Sub(w.PolledAt) < s.interval(*w) {
		return s.offer(ctx, w.ID, res)
	}
	// The lease is this binary's own, not the session's: a slow tick and
	// the next one share the session's process but are two binaries.
	holder := s.self
	if holder.PID == 0 {
		holder = self
	}
	var taken bool
	err = s.store.Tx(ctx, func(tx Tx) error {
		taken, err = tx.TakeLease(w.ID, holder, now.Add(domain.LeaseFor), s.leaseFree)
		return err
	})
	if err != nil {
		return res, err
	}
	if taken {
		pollCtx, cancel := context.WithTimeout(ctx, domain.PollBudget)
		err = s.poll(pollCtx, *w)
		cancel()
		if rerr := s.store.Tx(ctx, func(tx Tx) error { return tx.ReleaseLease(w.ID, holder) }); err == nil {
			err = rerr
		}
		if err != nil {
			return res, err
		}
		res.Polled = true
	}
	return s.offer(ctx, w.ID, res)
}

// leaseFree says whether a lease can be taken: its expiry passed or its
// process is gone.
func (s *Service) leaseFree(l Lease) bool {
	if !s.clock.Now().Before(l.ExpiresAt) {
		return true
	}
	start, running, err := s.processes.StartOf(l.Process.PID)
	return err == nil && (!running || start != l.Process.Start)
}

// offer builds or re-offers the watch's batch, unless it is paused.
func (s *Service) offer(ctx context.Context, watchID int64, res NextResult) (NextResult, error) {
	err := s.store.Tx(ctx, func(tx Tx) error {
		w, err := tx.Watch(watchID)
		if err != nil || w == nil {
			return err
		}
		if res.Status, err = s.lineState(tx, *w); err != nil {
			return err
		}
		if w.PausedSince != nil {
			return nil
		}
		pending, err := tx.Changes(ChangeQuery{WatchID: w.ID, Status: []domain.Status{domain.StatusPending}})
		if err != nil || len(pending) == 0 {
			return err
		}
		b, err := tx.OpenBatch(w.ID)
		if err != nil {
			return err
		}
		ids := make([]int64, len(pending))
		for i, c := range pending {
			ids[i] = c.ID
		}
		now := s.clock.Now()
		if b == nil || !sameIDs(b.Changes, ids) {
			// New changes since the batch was offered: a new batch holding
			// everything pending takes its place, so one wake says it all. The
			// old batch keeps its changes, so it can still be read and acked.
			b = &Batch{WatchID: w.ID, Changes: ids, OfferedAt: now}
			if err := tx.AddBatch(b); err != nil {
				return err
			}
			if err := tx.SetChangeStatus(ids, domain.StatusPending, b.ID); err != nil {
				return err
			}
		} else {
			b.OfferedAt = now
			if err := tx.SaveBatch(*b); err != nil {
				return err
			}
		}
		changes := make([]domain.Change, len(pending))
		for i, c := range pending {
			changes[i] = c.Change
		}
		res.Batch = &Offer{ID: b.ID, Wake: domain.Wake(b.ID, changes), Count: len(changes)}
		return nil
	})
	return res, err
}

func sameIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[int64]bool{}
	for _, x := range a {
		seen[x] = true
	}
	for _, y := range b {
		if !seen[y] {
			return false
		}
	}
	return true
}

func (s *Service) lineState(tx Tx, w Watch) (*LineState, error) {
	ls := &LineState{Watch: w.Repo.String(), PausedSince: w.PausedSince, Problems: []Issue{}}
	if !w.PolledAt.IsZero() {
		t := w.PolledAt
		ls.PolledAt = &t
	}
	held, err := tx.Changes(ChangeQuery{WatchID: w.ID, Status: []domain.Status{domain.StatusHeld}})
	if err != nil {
		return nil, err
	}
	pending, err := tx.Changes(ChangeQuery{WatchID: w.ID, Status: []domain.Status{domain.StatusPending}})
	if err != nil {
		return nil, err
	}
	ls.Held, ls.Pending = len(held), len(pending)
	recent, err := tx.Changes(ChangeQuery{WatchID: w.ID, Since: s.clock.Now().Add(-24 * time.Hour),
		Status: []domain.Status{domain.StatusPending, domain.StatusHeld, domain.StatusDelivered}})
	if err != nil {
		return nil, err
	}
	for _, c := range recent {
		if c.Change.Kind == domain.KindProblem {
			continue
		}
		if ls.LastChangeAt == nil || c.FoundAt.After(*ls.LastChangeAt) {
			t := c.FoundAt
			ls.LastChangeAt = &t
		}
	}
	probs, err := tx.OpenProblems(w.ID)
	if err != nil {
		return nil, err
	}
	for _, p := range probs {
		ls.Problems = append(ls.Problems, Issue{Code: p.Code, Text: p.Text})
	}
	return ls, nil
}

// ErrUnknownBatch is a batch id state.db does not hold.
var ErrUnknownBatch = errors.New("unknown batch")

// AckResult is what an ack did.
type AckResult struct {
	Batch     int64 `json:"batch"`
	Delivered int   `json:"delivered"`
	Already   bool  `json:"already,omitempty"`
}

// problemKey names the change that reported a problem: raise stores both
// with the same source, code and time.
type problemKey struct {
	source SourceName
	code   string
	at     int64 // UnixNano
}

// Ack marks a batch delivered.
func (s *Service) Ack(ctx context.Context, batch int64) (AckResult, error) {
	res := AckResult{Batch: batch}
	err := s.store.Tx(ctx, func(tx Tx) error {
		b, err := tx.Batch(batch)
		if err != nil {
			return err
		}
		if b == nil {
			return domain.Refuse("no_such_batch", fmt.Sprintf("no batch %d", batch), "`wakewire ledger` shows the batches")
		}
		if b.AckedAt != nil {
			res.Already = true
			return nil
		}
		now := s.clock.Now()
		b.AckedAt = &now
		if err := tx.SaveBatch(*b); err != nil {
			return err
		}
		cs, err := tx.Changes(ChangeQuery{IDs: b.Changes})
		if err != nil {
			return err
		}
		var ids []int64
		for _, c := range cs {
			if c.Change.Status == domain.StatusPending {
				ids = append(ids, c.ID)
			}
		}
		res.Delivered = len(ids)
		if err := tx.SetChangeStatus(ids, domain.StatusDelivered, b.ID); err != nil {
			return err
		}
		// Only the problems this batch reported are delivered: a problem
		// raised after the batch was offered waits for its own.
		reported := map[problemKey]bool{}
		for _, c := range cs {
			if c.Change.Kind == domain.KindProblem {
				reported[problemKey{c.Source, c.Change.Code, c.Change.At.UnixNano()}] = true
			}
		}
		probs, err := tx.OpenProblems(b.WatchID)
		if err != nil {
			return err
		}
		for _, p := range probs {
			if p.DeliveredAt == nil && reported[problemKey{p.Source, p.Code, p.Since.UnixNano()}] {
				p.DeliveredAt = &now
				if err := tx.SaveProblem(p); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return res, err
}

// EndedResult is what an ended report did.
type EndedResult struct {
	Session string `json:"session"`
	Dormant bool   `json:"dormant"`
	Watch   string `json:"watch,omitempty"`
}

// Ended records that a session ended, making its registration dormant.
func (s *Service) Ended(ctx context.Context, session, reason string) (EndedResult, error) {
	res := EndedResult{Session: session}
	err := s.store.Tx(ctx, func(tx Tx) error {
		if err := tx.DeleteAdapter(session); err != nil {
			return err
		}
		w, r, err := s.held(tx, session)
		if err != nil || r == nil {
			return err
		}
		now := s.clock.Now()
		r.EndedAt, r.EndReason = &now, reason
		res.Dormant, res.Watch = true, w.Repo.String()
		return tx.SaveRegistration(*r)
	})
	return res, err
}
