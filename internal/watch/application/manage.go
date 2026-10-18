package application

import (
	"context"
	"errors"
	"time"

	"github.com/gperezmz/wakewire/internal/watch/domain"
)

// PauseResult is what pause or resume left.
type PauseResult struct {
	Watch       string     `json:"watch"`
	Paused      bool       `json:"paused"`
	PausedSince *time.Time `json:"pausedSince"`
	Until       *time.Time `json:"until"`
	Released    int        `json:"released,omitempty"` // held changes made pending by a resume
	Already     bool       `json:"already,omitempty"`
}

// Pause keeps polling and storing changes but delivers none, for d or
// until resumed.
func (s *Service) Pause(ctx context.Context, sel Selector, d time.Duration, dryRun bool) (PauseResult, error) {
	var res PauseResult
	err := s.store.Tx(ctx, func(tx Tx) error {
		w, err := s.pick(ctx, tx, sel)
		if err != nil {
			return err
		}
		now := s.clock.Now()
		res.Watch = w.Repo.String()
		// A pause already on keeps its end unless --for names a new one, so a
		// second bare pause changes nothing.
		switch {
		case w.PausedSince == nil:
			w.PausedSince = &now
			w.PausedUntil = nil
			if d > 0 {
				until := now.Add(d)
				w.PausedUntil = &until
			}
		case d > 0:
			until := now.Add(d)
			w.PausedUntil = &until
		default:
			res.Already = true
		}
		res.Paused, res.PausedSince, res.Until = true, w.PausedSince, w.PausedUntil
		if dryRun {
			return nil
		}
		return tx.SaveWatch(w)
	})
	return res, err
}

// Resume makes every held change pending, so the next tick delivers them
// as one wake.
func (s *Service) Resume(ctx context.Context, sel Selector, dryRun bool) (PauseResult, error) {
	var res PauseResult
	err := s.store.Tx(ctx, func(tx Tx) error {
		w, err := s.pick(ctx, tx, sel)
		if err != nil {
			return err
		}
		res.Watch = w.Repo.String()
		if w.PausedSince == nil {
			res.Already = true
			return nil
		}
		n, err := s.release(tx, w, dryRun)
		res.Released = n
		return err
	})
	return res, err
}

func (s *Service) release(tx Tx, w *Watch, dryRun bool) (int, error) {
	held, err := tx.Changes(ChangeQuery{WatchID: w.ID, Status: []domain.Status{domain.StatusHeld}})
	if err != nil || dryRun {
		return len(held), err
	}
	ids := make([]int64, len(held))
	for i, c := range held {
		ids[i] = c.ID
	}
	if err := tx.SetChangeStatus(ids, domain.StatusPending, 0); err != nil {
		return 0, err
	}
	w.PausedSince, w.PausedUntil = nil, nil
	return len(held), tx.SaveWatch(w)
}

// endPauseIfDue resumes a watch whose `pause --for` ran out.
func (s *Service) endPauseIfDue(ctx context.Context, watchID int64) error {
	return s.store.Tx(ctx, func(tx Tx) error {
		w, err := tx.Watch(watchID)
		if err != nil || w == nil || w.PausedUntil == nil || s.clock.Now().Before(*w.PausedUntil) {
			return err
		}
		_, err = s.release(tx, w, false)
		return err
	})
}

// IntervalResult is a watch's interval after the command.
type IntervalResult struct {
	Watch    string `json:"watch"`
	Interval string `json:"interval"`
	Default  bool   `json:"default"`
	Already  bool   `json:"already,omitempty"`
}

// SetInterval sets how often a watch is polled; 0 returns it to the
// configuration's value.
func (s *Service) SetInterval(ctx context.Context, sel Selector, d time.Duration, dryRun bool) (IntervalResult, error) {
	var res IntervalResult
	err := s.store.Tx(ctx, func(tx Tx) error {
		w, err := s.pick(ctx, tx, sel)
		if err != nil {
			return err
		}
		already := w.Interval == d
		w.Interval = d
		res = IntervalResult{Watch: w.Repo.String(), Interval: s.interval(*w).String(), Default: d == 0, Already: already}
		if dryRun || already {
			return nil
		}
		return tx.SaveWatch(w)
	})
	return res, err
}

// UnregisterResult says what was forgotten.
type UnregisterResult struct {
	Watch    string `json:"watch"`
	Identity string `json:"identity"`
	Removed  bool   `json:"removed"`
	Already  bool   `json:"already,omitempty"`
}

// Unregister forgets a watch: its registration, snapshots and undelivered
// changes. The ledger keeps what was recorded until it ages out.
func (s *Service) Unregister(ctx context.Context, sel Selector, dryRun bool) (UnregisterResult, error) {
	var res UnregisterResult
	err := s.store.Tx(ctx, func(tx Tx) error {
		w, err := s.pick(ctx, tx, sel)
		var r *domain.Refusal
		if errors.As(err, &r) && r.Code() == "not_registered" {
			res.Already = true // safe to repeat: nothing left to forget
			return nil
		}
		if err != nil {
			return err
		}
		res = UnregisterResult{Watch: w.Repo.String(), Identity: w.Identity, Removed: !dryRun}
		if dryRun {
			return nil
		}
		return tx.DeleteWatch(w.ID)
	})
	return res, err
}
