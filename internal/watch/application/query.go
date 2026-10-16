package application

import (
	"context"
	"fmt"
	"time"

	"github.com/gperezmz/wakewire/internal/watch/domain"
)

// WatchStatus is one watch as `wakewire status` shows it.
type WatchStatus struct {
	Watch       string         `json:"watch"`
	Identity    string         `json:"identity"`
	Tracker     string         `json:"tracker"`
	Holder      *HolderStatus  `json:"holder"`
	PolledAt    *time.Time     `json:"polledAt"`
	NextPollAt  *time.Time     `json:"nextPollAt"`
	Interval    string         `json:"interval"`
	Sources     []SourceStatus `json:"sources"`
	Pending     int            `json:"pending"`
	Held        int            `json:"held"`
	PausedSince *time.Time     `json:"pausedSince"`
	PausedUntil *time.Time     `json:"pausedUntil"`
	Problems    []ProblemView  `json:"problems"`
}

// HolderStatus is who holds a watch and whether that still holds.
type HolderStatus struct {
	Holder  string    `json:"holder"`
	PID     int       `json:"pid"`
	HeardAt time.Time `json:"heardAt"`
	State   string    `json:"state"` // held, dormant
	Why     string    `json:"why,omitempty"`
}

// SourceStatus is one half of a watch.
type SourceStatus struct {
	Name     SourceName  `json:"name"`
	Service  string      `json:"service"`
	Account  string      `json:"account"`
	Identity string      `json:"identity"`
	Info     AccountInfo `json:"info"`
	Rate     *RateInfo   `json:"rate"`
	OKAt     *time.Time  `json:"okAt"`
}

// ProblemView is an open problem, or one cleared lately.
type ProblemView struct {
	Source    SourceName `json:"source"`
	Code      string     `json:"code"`
	Text      string     `json:"text"`
	Fix       string     `json:"fix"`
	Since     time.Time  `json:"since"`
	ClearedAt *time.Time `json:"clearedAt,omitempty"`
}

// Status answers every watch, or the one sel names.
func (s *Service) Status(ctx context.Context, sel *Selector) ([]WatchStatus, error) {
	var out []WatchStatus
	err := s.store.Tx(ctx, func(tx Tx) error {
		var watches []Watch
		if sel != nil {
			w, err := s.pick(ctx, tx, *sel)
			if err != nil {
				return err
			}
			watches = []Watch{*w}
		} else {
			all, err := tx.Watches()
			if err != nil {
				return err
			}
			watches = all
		}
		for _, w := range watches {
			st, err := s.status1(ctx, tx, w)
			if err != nil {
				return err
			}
			out = append(out, st)
		}
		return nil
	})
	return out, err
}

func (s *Service) status1(ctx context.Context, tx Tx, w Watch) (WatchStatus, error) {
	st := WatchStatus{Watch: w.Repo.String(), Identity: w.Identity, Tracker: w.Tracker.String(),
		Interval: s.interval(w).String(), PausedSince: w.PausedSince, PausedUntil: w.PausedUntil,
		Sources: []SourceStatus{}, Problems: []ProblemView{}}
	if !w.PolledAt.IsZero() {
		p, n := w.PolledAt, w.PolledAt.Add(s.interval(w))
		st.PolledAt, st.NextPollAt = &p, &n
	}
	if r, err := tx.Registration(w.ID); err != nil {
		return st, err
	} else if r != nil {
		holds, why, err := s.liveness(tx, w, *r)
		if err != nil {
			return st, err
		}
		h := &HolderStatus{Holder: r.Harness + ":" + r.Session, PID: r.Process.PID, HeardAt: r.HeardAt, State: "held"}
		if !holds {
			h.State, h.Why = "dormant", why
		}
		st.Holder = h
	}
	halves := []SourceStatus{{Name: SourceForge, Account: w.ForgeAccount, Identity: w.Identity}}
	if w.Tracker.Kind == domain.TrackerJira {
		halves = append(halves, SourceStatus{Name: SourceTracker, Service: "Jira", Account: w.TrackerAccount, Identity: w.TrackerIdentity})
	}
	for _, h := range halves {
		if h.Service == "" {
			h.Service = forgeService(w)
		}
		info, err := s.accounts.Describe(ctx, h.Account)
		if err == nil {
			h.Info = info
		}
		ss, err := tx.SourceState(w.ID, h.Name)
		if err != nil {
			return st, err
		}
		if ss.Rate.Limit > 0 || ss.Rate.NearLimit {
			r := ss.Rate
			h.Rate = &r
		}
		if !ss.OKAt.IsZero() {
			t := ss.OKAt
			h.OKAt = &t
		}
		st.Sources = append(st.Sources, h)
	}
	ls, err := s.lineState(tx, w)
	if err != nil {
		return st, err
	}
	st.Pending, st.Held = ls.Pending, ls.Held
	probs, err := tx.OpenProblems(w.ID)
	if err != nil {
		return st, err
	}
	for _, p := range probs {
		st.Problems = append(st.Problems, ProblemView{Source: p.Source, Code: p.Code, Text: p.Text, Fix: p.Hint, Since: p.Since})
	}
	return st, nil
}

func forgeService(w Watch) string {
	if w.Repo.Host == "github.com" {
		return "GitHub"
	}
	return "GitLab"
}

// LedgerEntry is one stored change and what was done with it, never what
// anyone wrote.
type LedgerEntry struct {
	ID      int64         `json:"id"`
	Source  SourceName    `json:"source"`
	Item    string        `json:"item"`
	Kind    domain.Kind   `json:"kind"`
	By      string        `json:"by,omitempty"`
	At      time.Time     `json:"at"`
	FoundAt time.Time     `json:"foundAt"`
	Status  domain.Status `json:"status"`
	Reason  string        `json:"reason,omitempty"`
	Batch   int64         `json:"batch,omitempty"`
	Label   string        `json:"label,omitempty"`
	Code    string        `json:"code,omitempty"`
}

// LedgerResult is a watch's ledger.
type LedgerResult struct {
	Watch   string        `json:"watch"`
	Changes []LedgerEntry `json:"changes"`
}

// Ledger answers every change stored for a watch.
func (s *Service) Ledger(ctx context.Context, sel Selector, since time.Duration, item string) (LedgerResult, error) {
	var res LedgerResult
	err := s.store.Tx(ctx, func(tx Tx) error {
		w, err := s.pick(ctx, tx, sel)
		if err != nil {
			return err
		}
		res.Watch = w.Repo.String()
		q := ChangeQuery{WatchID: w.ID, Item: item}
		if since > 0 {
			q.Since = s.clock.Now().Add(-since)
		}
		cs, err := tx.Changes(q)
		if err != nil {
			return err
		}
		res.Changes = make([]LedgerEntry, 0, len(cs))
		for _, c := range cs {
			res.Changes = append(res.Changes, LedgerEntry{ID: c.ID, Source: c.Source, Item: c.Change.Item, Kind: c.Change.Kind,
				By: c.Change.By, At: c.Change.At, FoundAt: c.FoundAt, Status: c.Change.Status, Reason: c.Change.Reason,
				Batch: c.Batch, Label: c.Change.Label, Code: c.Change.Code})
		}
		return nil
	})
	return res, err
}

// BatchView is a batch in the wake contract's format.
type BatchView struct {
	Protocol int             `json:"protocol"`
	Batch    int64           `json:"batch"`
	Watch    string          `json:"watch"`
	Identity string          `json:"identity"`
	Since    time.Time       `json:"since"`
	Until    time.Time       `json:"until"`
	Changes  []domain.Change `json:"changes"`
}

// Changes answers the full batch, including what people wrote.
func (s *Service) Changes(ctx context.Context, batch int64) (BatchView, error) {
	var v BatchView
	err := s.store.Tx(ctx, func(tx Tx) error {
		b, err := tx.Batch(batch)
		if err != nil {
			return err
		}
		if b == nil {
			return domain.Invalid(fmt.Sprintf("no batch %d", batch), "the wake names its batch; `wakewire ledger` shows the batches")
		}
		w, err := tx.Watch(b.WatchID)
		if err != nil {
			return err
		}
		cs, err := tx.Changes(ChangeQuery{IDs: b.Changes})
		if err != nil {
			return err
		}
		v = BatchView{Protocol: domain.Protocol, Batch: b.ID, Until: b.OfferedAt, Changes: make([]domain.Change, 0, len(cs))}
		if w != nil {
			v.Watch, v.Identity = w.Repo.String(), w.Identity
		}
		for i, c := range cs {
			if i == 0 || c.Change.At.Before(v.Since) {
				v.Since = c.Change.At
			}
			v.Changes = append(v.Changes, c.Change)
		}
		return nil
	})
	return v, err
}
