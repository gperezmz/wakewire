package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gperezmz/wakewire/internal/watch/domain"
)

// poll reads each of the watch's sources and records what it found. Each
// source's changes and snapshots are written in one transaction, so a
// change is stored exactly when its snapshot moves past it.
func (s *Service) poll(ctx context.Context, w Watch) error {
	sources, err := s.sources.For(ctx, w)
	if err != nil {
		var c interface{ Code() string }
		if errors.As(err, &c) {
			return s.recordFailure(ctx, w, SourceForge, "the forge", err)
		}
		return err
	}
	firstPoll := w.PolledAt.IsZero()
	for _, src := range sources {
		if err := s.pollOne(ctx, w, src, firstPoll); err != nil {
			return err
		}
	}
	return s.store.Tx(ctx, func(tx Tx) error {
		cur, err := tx.Watch(w.ID)
		if err != nil || cur == nil {
			return err
		}
		cur.PolledAt = s.clock.Now()
		return tx.SaveWatch(cur)
	})
}

func (s *Service) pollOne(ctx context.Context, w Watch, src BoundSource, firstPoll bool) error {
	var state SourceState
	var snaps map[string]domain.Snapshot
	err := s.store.Tx(ctx, func(tx Tx) error {
		var err error
		if state, err = tx.SourceState(w.ID, src.Name); err != nil {
			return err
		}
		snaps, err = tx.Snapshots(w.ID, src.Name)
		return err
	})
	if err != nil {
		return err
	}
	if s.clock.Now().Before(state.SkipUntil) {
		return nil
	}
	out, err := src.Source.Poll(ctx, PollInput{Rules: src.Rules, IncludeIssues: src.IncludeIssues, FirstPoll: firstPoll, Snapshots: snaps, State: state.Data})
	if err != nil {
		var c interface{ Code() string }
		if errors.As(err, &c) {
			return s.recordFailure(ctx, w, src.Name, src.Source.Service(), err)
		}
		return err
	}
	return s.store.Tx(ctx, func(tx Tx) error {
		cur, err := tx.Watch(w.ID)
		if err != nil || cur == nil {
			return err
		}
		paused := cur.PausedSince != nil
		since := w.PolledAt
		if firstPoll {
			since = time.Time{}
		}
		observed := map[string]bool{}
		for _, obs := range out.Observations {
			observed[obs.Item.Ref] = true
			var prev *domain.Snapshot
			if p, ok := snaps[obs.Item.Ref]; ok {
				prev = &p
			}
			d := domain.Diff(src.Rules, prev, obs, since)
			if err := s.store1(tx, w.ID, src.Name, d.Changes, paused); err != nil {
				return err
			}
			if d.Drop {
				if prev != nil {
					if err := tx.DeleteSnapshot(w.ID, src.Name, obs.Item.Ref); err != nil {
						return err
					}
				}
				continue
			}
			if err := tx.SaveSnapshot(w.ID, src.Name, d.Next); err != nil {
				return err
			}
		}
		for ref, snap := range snaps {
			if observed[ref] {
				continue
			}
			cs, next := domain.Settle(src.Rules, snap)
			if len(cs) == 0 {
				continue
			}
			if err := s.store1(tx, w.ID, src.Name, cs, paused); err != nil {
				return err
			}
			if err := tx.SaveSnapshot(w.ID, src.Name, next); err != nil {
				return err
			}
		}
		state.WatchID, state.Source = w.ID, src.Name
		state.Data, state.Rate, state.Failures, state.OKAt = out.State, out.Rate, 0, s.clock.Now()
		if out.Rate.Limit > 0 && out.Rate.Remaining*20 < out.Rate.Limit {
			state.SkipUntil = out.Rate.ResetAt
			if err := s.raise(tx, w, src.Name, "rate_limited", fmt.Sprintf(
				"not watching %s until %s: its rate limit is nearly spent (%d of %d left).",
				src.Source.Service(), out.Rate.ResetAt.Local().Format("15:04"), out.Rate.Remaining, out.Rate.Limit),
				"wait for the reset, or lengthen the interval: `wakewire interval 5m`", paused); err != nil {
				return err
			}
		} else if err := s.clear(tx, w.ID, src.Name); err != nil {
			return err
		}
		return tx.SaveSourceState(state)
	})
}

// store1 stores changes; a change that would wake is held while paused.
func (s *Service) store1(tx Tx, watchID int64, source SourceName, cs []domain.Change, paused bool) error {
	now := s.clock.Now()
	for _, c := range cs {
		if c.Status == domain.StatusPending && paused {
			c.Status = domain.StatusHeld
		}
		if _, err := tx.AddChange(StoredChange{WatchID: watchID, Source: source, Change: c, FoundAt: now}); err != nil {
			return err
		}
	}
	return nil
}

// recordFailure counts a failed poll and raises a problem: at once for a
// refused token or a spent budget, after three polls in a row for a
// service that did not answer.
func (s *Service) recordFailure(ctx context.Context, w Watch, name SourceName, service string, failure error) error {
	var c interface {
		Code() string
		Hint() string
		Error() string
	}
	if !errors.As(failure, &c) {
		return failure
	}
	var reset interface{ Reset() time.Time }
	return s.store.Tx(ctx, func(tx Tx) error {
		cur, err := tx.Watch(w.ID)
		if err != nil || cur == nil {
			return err
		}
		state, err := tx.SourceState(w.ID, name)
		if err != nil {
			return err
		}
		state.WatchID, state.Source = w.ID, name
		state.Failures++
		raise := true
		switch c.Code() {
		case "unreachable":
			raise = state.Failures >= domain.UnreachableAt
		case "rate_limited":
			if errors.As(failure, &reset) {
				state.SkipUntil = reset.Reset()
			}
		}
		if raise {
			text := fmt.Sprintf("not watching %s. %s.", service, trimDot(c.Error()))
			if err := s.raise(tx, *cur, name, c.Code(), text, c.Hint(), cur.PausedSince != nil); err != nil {
				return err
			}
		}
		return tx.SaveSourceState(state)
	})
}

// raise records a problem once: a new problem is stored as a change of its
// own, delivered once; one already open is left as it is.
func (s *Service) raise(tx Tx, w Watch, name SourceName, code, text, hint string, paused bool) error {
	open, err := tx.OpenProblems(w.ID)
	if err != nil {
		return err
	}
	for _, p := range open {
		if p.Source == name && p.Code == code {
			return nil
		}
	}
	now := s.clock.Now()
	if err := tx.AddProblem(&Problem{WatchID: w.ID, Source: name, Code: code, Text: text, Hint: hint, Since: now}); err != nil {
		return err
	}
	ch := domain.Change{Item: w.Repo.String(), URL: "https://" + w.Repo.String(), Kind: domain.KindProblem, At: now, Code: code, Text: text,
		Status: domain.StatusPending}
	return s.store1(tx, w.ID, name, []domain.Change{ch}, paused)
}

// clear ends a source's open problems: a poll succeeded.
func (s *Service) clear(tx Tx, watchID int64, name SourceName) error {
	open, err := tx.OpenProblems(watchID)
	if err != nil {
		return err
	}
	now := s.clock.Now()
	for _, p := range open {
		if p.Source == name {
			p.ClearedAt = &now
			if err := tx.SaveProblem(p); err != nil {
				return err
			}
		}
	}
	return nil
}

func trimDot(s string) string {
	for len(s) > 0 && s[len(s)-1] == '.' {
		s = s[:len(s)-1]
	}
	return s
}
