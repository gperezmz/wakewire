// Package fakes holds in-memory stand-ins for the watch context's ports,
// for its use case tests.
package fakes

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gperezmz/wakewire/internal/watch/application"
	"github.com/gperezmz/wakewire/internal/watch/domain"
)

type snapKey struct {
	Watch  int64
	Source application.SourceName
	Ref    string
}

type sourceKey struct {
	Watch  int64
	Source application.SourceName
}

type state struct {
	NextID        int64
	Watches       map[int64]application.Watch
	Registrations map[int64]application.Registration
	Adapters      map[string]application.Adapter
	Snapshots     map[string]domain.Snapshot // by snapKey as JSON
	Changes       map[int64]application.StoredChange
	Batches       map[int64]application.Batch
	Problems      map[int64]application.Problem
	Sources       map[string]application.SourceState
	Leases        map[int64]application.Lease
	PrunedAt      time.Time
}

// Store is an in-memory state.db. Each transaction works on a copy that
// replaces the state only when the transaction returns no error.
type Store struct {
	mu sync.Mutex
	s  state
	// FailCommit, when set, fails the next commit, as a killed process would.
	FailCommit bool
	commits    int // transactions that committed or were made to fail, counted from 1
	failAt     int // the commit number to fail; 0: none
	open       atomic.Int32
}

// Commits answers how many transactions have reached their commit.
func (st *Store) Commits() int { st.mu.Lock(); defer st.mu.Unlock(); return st.commits }

// FailCommitNumber fails the n-th commit counted from the store's start, as
// a killed process would: whatever that transaction wrote is gone. A test
// that kills the process at each commit of a use case in turn shows what
// has to be written together.
func (st *Store) FailCommitNumber(n int) { st.mu.Lock(); st.failAt = n; st.mu.Unlock() }

// InTx says whether a transaction is open now: a transaction holds
// state.db's write lock, so a call that waits on the network must not be
// made while it is.
func (st *Store) InTx() bool { return st.open.Load() > 0 }

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{s: state{NextID: 1, Watches: map[int64]application.Watch{}, Registrations: map[int64]application.Registration{},
		Adapters: map[string]application.Adapter{}, Snapshots: map[string]domain.Snapshot{}, Changes: map[int64]application.StoredChange{},
		Batches: map[int64]application.Batch{}, Problems: map[int64]application.Problem{}, Sources: map[string]application.SourceState{},
		Leases: map[int64]application.Lease{}}}
}

// ensureMaps puts back the maps gob leaves nil when they were empty.
func ensureMaps(s *state) {
	if s.Watches == nil {
		s.Watches = map[int64]application.Watch{}
	}
	if s.Registrations == nil {
		s.Registrations = map[int64]application.Registration{}
	}
	if s.Adapters == nil {
		s.Adapters = map[string]application.Adapter{}
	}
	if s.Snapshots == nil {
		s.Snapshots = map[string]domain.Snapshot{}
	}
	if s.Changes == nil {
		s.Changes = map[int64]application.StoredChange{}
	}
	if s.Batches == nil {
		s.Batches = map[int64]application.Batch{}
	}
	if s.Problems == nil {
		s.Problems = map[int64]application.Problem{}
	}
	if s.Sources == nil {
		s.Sources = map[string]application.SourceState{}
	}
	if s.Leases == nil {
		s.Leases = map[int64]application.Lease{}
	}
}

type crashed struct{}

func (crashed) Error() string { return "the process was killed before the commit" }

// Tx runs fn over a copy of the state.
func (st *Store) Tx(_ context.Context, fn func(application.Tx) error) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.open.Add(1)
	defer st.open.Add(-1)
	var cp state
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(st.s); err != nil {
		return err
	}
	if err := gob.NewDecoder(&buf).Decode(&cp); err != nil {
		return err
	}
	ensureMaps(&cp)
	if err := fn(&tx{&cp}); err != nil {
		return err
	}
	st.commits++
	if st.FailCommit || st.commits == st.failAt {
		st.FailCommit = false
		return crashed{}
	}
	st.s = cp
	return nil
}

func key(v any) string { b, _ := json.Marshal(v); return string(b) }

type tx struct{ s *state }

func (x *tx) id() int64 { x.s.NextID++; return x.s.NextID - 1 }

func (x *tx) Watches() ([]application.Watch, error) {
	var out []application.Watch
	for _, w := range x.s.Watches {
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (x *tx) Watch(id int64) (*application.Watch, error) {
	if w, ok := x.s.Watches[id]; ok {
		return &w, nil
	}
	return nil, nil
}

func (x *tx) WatchByKey(repo, identity string) (*application.Watch, error) {
	for _, w := range x.s.Watches {
		if w.Repo.String() == repo && w.Identity == identity {
			return &w, nil
		}
	}
	return nil, nil
}

func (x *tx) SaveWatch(w *application.Watch) error {
	if w.ID == 0 {
		w.ID = x.id()
	}
	x.s.Watches[w.ID] = *w
	return nil
}

func (x *tx) DeleteWatch(id int64) error {
	delete(x.s.Watches, id)
	delete(x.s.Registrations, id)
	delete(x.s.Leases, id)
	for k, c := range x.s.Changes {
		if c.WatchID == id && (c.Change.Status == domain.StatusPending || c.Change.Status == domain.StatusHeld) {
			delete(x.s.Changes, k)
		}
	}
	for k := range x.s.Snapshots {
		var sk snapKey
		_ = json.Unmarshal([]byte(k), &sk)
		if sk.Watch == id {
			delete(x.s.Snapshots, k)
		}
	}
	for k, p := range x.s.Problems {
		if p.WatchID == id {
			delete(x.s.Problems, k)
		}
	}
	return nil
}

func (x *tx) Registration(watchID int64) (*application.Registration, error) {
	if r, ok := x.s.Registrations[watchID]; ok {
		return &r, nil
	}
	return nil, nil
}

func (x *tx) latest(ok func(application.Registration) bool) *application.Registration {
	var best *application.Registration
	for _, r := range x.s.Registrations {
		if ok(r) && (best == nil || r.RegisteredAt.After(best.RegisteredAt)) {
			r := r
			best = &r
		}
	}
	return best
}

func (x *tx) RegistrationBySession(session string) (*application.Registration, error) {
	return x.latest(func(r application.Registration) bool { return r.Session == session }), nil
}

func (x *tx) RegistrationReplacing(session string) (*application.Registration, error) {
	return x.latest(func(r application.Registration) bool { return r.Replaced == session }), nil
}

func (x *tx) SaveRegistration(r application.Registration) error {
	x.s.Registrations[r.WatchID] = r
	return nil
}

func (x *tx) Adapter(session string) (*application.Adapter, error) {
	if a, ok := x.s.Adapters[session]; ok {
		return &a, nil
	}
	return nil, nil
}

func (x *tx) AdaptersOf(p application.Process) ([]application.Adapter, error) {
	var out []application.Adapter
	for _, a := range x.s.Adapters {
		if a.Process == p {
			out = append(out, a)
		}
	}
	return out, nil
}

func (x *tx) SaveAdapter(a application.Adapter) error { x.s.Adapters[a.Session] = a; return nil }

func (x *tx) DeleteAdapter(session string) error { delete(x.s.Adapters, session); return nil }

func (x *tx) Snapshots(watchID int64, source application.SourceName) (map[string]domain.Snapshot, error) {
	out := map[string]domain.Snapshot{}
	for k, s := range x.s.Snapshots {
		var sk snapKey
		_ = json.Unmarshal([]byte(k), &sk)
		if sk.Watch == watchID && sk.Source == source {
			out[sk.Ref] = s
		}
	}
	return out, nil
}

func (x *tx) SaveSnapshot(watchID int64, source application.SourceName, s domain.Snapshot) error {
	x.s.Snapshots[key(snapKey{watchID, source, s.Item.Ref})] = s
	return nil
}

func (x *tx) DeleteSnapshot(watchID int64, source application.SourceName, ref string) error {
	delete(x.s.Snapshots, key(snapKey{watchID, source, ref}))
	return nil
}

func (x *tx) AddChange(c application.StoredChange) (int64, error) {
	c.ID = x.id()
	x.s.Changes[c.ID] = c
	return c.ID, nil
}

func (x *tx) Changes(q application.ChangeQuery) ([]application.StoredChange, error) {
	var out []application.StoredChange
	for _, c := range x.s.Changes {
		switch {
		case q.WatchID != 0 && c.WatchID != q.WatchID,
			len(q.Status) > 0 && !slices.Contains(q.Status, c.Change.Status),
			!q.Since.IsZero() && c.FoundAt.Before(q.Since),
			q.Item != "" && c.Change.Item != q.Item,
			q.Batch != 0 && c.Batch != q.Batch,
			q.IDs != nil && !slices.Contains(q.IDs, c.ID):
			continue
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (x *tx) SetChangeStatus(ids []int64, status domain.Status, batch int64) error {
	for _, id := range ids {
		c := x.s.Changes[id]
		c.Change.Status = status
		if batch != 0 {
			c.Batch = batch
		}
		x.s.Changes[id] = c
	}
	return nil
}

func (x *tx) OpenBatch(watchID int64) (*application.Batch, error) {
	var best *application.Batch
	for _, b := range x.s.Batches {
		if b.WatchID == watchID && b.AckedAt == nil && (best == nil || b.ID > best.ID) {
			b := b
			best = &b
		}
	}
	return best, nil
}

func (x *tx) AddBatch(b *application.Batch) error {
	b.ID = x.id()
	x.s.Batches[b.ID] = *b
	return nil
}

func (x *tx) Batch(id int64) (*application.Batch, error) {
	if b, ok := x.s.Batches[id]; ok {
		return &b, nil
	}
	return nil, nil
}

func (x *tx) SaveBatch(b application.Batch) error { x.s.Batches[b.ID] = b; return nil }

func (x *tx) OpenProblems(watchID int64) ([]application.Problem, error) {
	var out []application.Problem
	for _, p := range x.s.Problems {
		if p.WatchID == watchID && p.ClearedAt == nil {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (x *tx) AddProblem(p *application.Problem) error {
	p.ID = x.id()
	x.s.Problems[p.ID] = *p
	return nil
}

func (x *tx) SaveProblem(p application.Problem) error { x.s.Problems[p.ID] = p; return nil }

func (x *tx) SourceState(watchID int64, source application.SourceName) (application.SourceState, error) {
	if s, ok := x.s.Sources[key(sourceKey{watchID, source})]; ok {
		return s, nil
	}
	return application.SourceState{WatchID: watchID, Source: source}, nil
}

func (x *tx) SaveSourceState(s application.SourceState) error {
	x.s.Sources[key(sourceKey{s.WatchID, s.Source})] = s
	return nil
}

func (x *tx) TakeLease(watchID int64, holder application.Process, until time.Time, free func(application.Lease) bool) (bool, error) {
	if l, ok := x.s.Leases[watchID]; ok && l.Holder != holder && !free(l) {
		return false, nil
	}
	x.s.Leases[watchID] = application.Lease{WatchID: watchID, Holder: holder, ExpiresAt: until}
	return true, nil
}

func (x *tx) ReleaseLease(watchID int64, holder application.Process) error {
	if l, ok := x.s.Leases[watchID]; ok && l.Holder == holder {
		delete(x.s.Leases, watchID)
	}
	return nil
}

func (x *tx) PrunedAt() (time.Time, error) { return x.s.PrunedAt, nil }

func (x *tx) Prune(p application.PrunePlan, dryRun bool) (application.PruneCounts, error) {
	var c application.PruneCounts
	for id, ch := range x.s.Changes {
		if (ch.Change.Status == domain.StatusDelivered || ch.Change.Status == domain.StatusIgnored) && ch.FoundAt.Before(p.Before) {
			c.Changes++
			if !dryRun {
				delete(x.s.Changes, id)
			}
		}
	}
	for id, b := range x.s.Batches {
		if b.AckedAt != nil && b.AckedAt.Before(p.Before) {
			c.Batches++
			if !dryRun {
				delete(x.s.Batches, id)
			}
		}
	}
	for id, pr := range x.s.Problems {
		if pr.ClearedAt != nil && pr.ClearedAt.Before(p.Before) {
			c.Problems++
			if !dryRun {
				delete(x.s.Problems, id)
			}
		}
	}
	for id, r := range x.s.Registrations {
		last := r.HeardAt
		if r.EndedAt != nil {
			last = *r.EndedAt
		}
		if last.Before(p.Before) && p.Dormant(r) {
			c.Registrations++
			c.Watches++
			if !dryRun {
				_ = x.DeleteWatch(id)
			}
		}
	}
	if !dryRun {
		x.s.PrunedAt = p.Now
	}
	return c, nil
}
