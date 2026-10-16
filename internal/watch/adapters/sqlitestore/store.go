// Package sqlitestore keeps state.db: SQLite through the pure-Go
// modernc.org/sqlite, in WAL mode with a busy timeout, so several sessions'
// binaries read and write it at once; see docs/reference/state.md.
package sqlitestore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite" // the database/sql driver "sqlite"

	"github.com/gperezmz/wakewire/internal/watch/application"
	"github.com/gperezmz/wakewire/internal/watch/domain"
)

// Store is state.db.
type Store struct{ db *sql.DB }

// Open opens state.db at path, creating it with mode 600, and migrates it.
func Open(ctx context.Context, path string) (*Store, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	_ = f.Close()
	s, err := open(ctx, "file:"+path+"?"+pragmas().Encode(), 0)
	if err != nil {
		return nil, fmt.Errorf("state.db at %s: %w", path, err)
	}
	return s, nil
}

// OpenMemory opens an empty state.db that lives in memory and leaves
// nothing on disk, for a command that must change no state on a machine
// where there is none yet.
func OpenMemory(ctx context.Context) (*Store, error) {
	// One connection: each connection to :memory: is a database of its own.
	return open(ctx, "file::memory:?"+pragmas().Encode(), 1)
}

func pragmas() url.Values {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(10000)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "auto_vacuum(incremental)")
	q.Add("_pragma", "journal_mode(wal)")
	q.Add("_pragma", "synchronous(normal)")
	q.Set("_txlock", "immediate")
	return q
}

func open(ctx context.Context, dsn string, maxConns int) (*Store, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if maxConns > 0 {
		db.SetMaxOpenConns(maxConns)
	}
	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	return s.Tx(ctx, func(application.Tx) error { return nil })
}

// Tx runs fn in one immediate transaction: every write in it commits
// together, or none does.
func (s *Store) Tx(ctx context.Context, fn func(application.Tx) error) error {
	t, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = t.Rollback() }()
	tx := &tx{t}
	if err := tx.ensureSchema(); err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	return t.Commit()
}

type tx struct{ t *sql.Tx }

func (x *tx) ensureSchema() error {
	var n int
	if err := x.t.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='meta'`).Scan(&n); err != nil {
		return err
	}
	v := 0
	if n > 0 {
		var s string
		err := x.t.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&s)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		v, _ = strconv.Atoi(s)
	}
	if v > schemaVersion {
		return fmt.Errorf("schema version %d is newer than this binary's %d: update wakewire", v, schemaVersion)
	}
	for ; v < schemaVersion; v++ {
		if _, err := x.t.Exec(migrations[v]); err != nil {
			return err
		}
		if _, err := x.t.Exec(`INSERT INTO meta(key, value) VALUES('schema_version', ?)
			ON CONFLICT(key) DO UPDATE SET value=excluded.value`, strconv.Itoa(v+1)); err != nil {
			return err
		}
	}
	return nil
}

// Times are stored as RFC 3339 text in UTC.
func ts(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func tsp(t *time.Time) any {
	if t == nil {
		return nil
	}
	return ts(*t)
}

func parseTS(s sql.NullString) time.Time {
	if !s.Valid || s.String == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339Nano, s.String)
	return t
}

func parseTSP(s sql.NullString) *time.Time {
	if !s.Valid || s.String == "" {
		return nil
	}
	t := parseTS(s)
	return &t
}

// nextID hands out an id for table that was never given out before, even
// when the row that held it is gone: meta keeps the highest, as an
// AUTOINCREMENT column would.
func (x *tx) nextID(name, table string) (int64, error) {
	key := name + "_id_max"
	var last, top int64
	var s string
	if err := x.t.QueryRow(`SELECT value FROM meta WHERE key=?`, key).Scan(&s); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	last, _ = strconv.ParseInt(s, 10, 64)
	if err := x.t.QueryRow(`SELECT coalesce(max(id), 0) FROM ` + table).Scan(&top); err != nil {
		return 0, err
	}
	id := max(last, top) + 1
	_, err := x.t.Exec(`INSERT INTO meta(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		key, strconv.FormatInt(id, 10))
	return id, err
}

// ---- watches

const watchCols = `id, repo, identity, forge_account, tracker, tracker_identity, tracker_account,
	labels, marker, interval_ms, paused_since, paused_until, polled_at, created_at`

type scanner interface{ Scan(...any) error }

func scanWatch(r scanner) (*application.Watch, error) {
	var w application.Watch
	var repo, tracker, labels string
	var intervalMS int64
	var pausedSince, pausedUntil, polledAt, createdAt sql.NullString
	err := r.Scan(&w.ID, &repo, &w.Identity, &w.ForgeAccount, &tracker, &w.TrackerIdentity, &w.TrackerAccount,
		&labels, &w.Marker, &intervalMS, &pausedSince, &pausedUntil, &polledAt, &createdAt)
	if err != nil {
		return nil, err
	}
	if w.Repo, err = domain.ParseRepo(repo); err != nil {
		return nil, err
	}
	if tracker != "" {
		if w.Tracker, err = domain.ParseTracker(tracker); err != nil {
			return nil, err
		}
	}
	if err := json.Unmarshal([]byte(labels), &w.Labels); err != nil {
		return nil, err
	}
	w.Interval = time.Duration(intervalMS) * time.Millisecond
	w.PausedSince, w.PausedUntil = parseTSP(pausedSince), parseTSP(pausedUntil)
	w.PolledAt, w.CreatedAt = parseTS(polledAt), parseTS(createdAt)
	return &w, nil
}

func (x *tx) Watches() ([]application.Watch, error) {
	rows, err := x.t.Query(`SELECT ` + watchCols + ` FROM watches ORDER BY repo, identity`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []application.Watch
	for rows.Next() {
		w, err := scanWatch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *w)
	}
	return out, rows.Err()
}

func noRow[T any](v *T, err error) (*T, error) {
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return v, err
}

func (x *tx) Watch(id int64) (*application.Watch, error) {
	return noRow(scanWatch(x.t.QueryRow(`SELECT `+watchCols+` FROM watches WHERE id=?`, id)))
}

func (x *tx) WatchByKey(repo, identity string) (*application.Watch, error) {
	return noRow(scanWatch(x.t.QueryRow(`SELECT `+watchCols+` FROM watches WHERE repo=? AND identity=?`, repo, identity)))
}

func (x *tx) SaveWatch(w *application.Watch) error {
	labels, err := json.Marshal(w.Labels)
	if err != nil {
		return err
	}
	var polled any
	if !w.PolledAt.IsZero() {
		polled = ts(w.PolledAt)
	}
	args := []any{w.Repo.String(), w.Identity, w.ForgeAccount, trackerText(w.Tracker), w.TrackerIdentity, w.TrackerAccount,
		string(labels), w.Marker, w.Interval.Milliseconds(), tsp(w.PausedSince), tsp(w.PausedUntil), polled, ts(w.CreatedAt)}
	if w.ID == 0 {
		id, err := x.nextID("watch", "watches")
		if err != nil {
			return err
		}
		_, err = x.t.Exec(`INSERT INTO watches(id, repo, identity, forge_account, tracker, tracker_identity, tracker_account,
			labels, marker, interval_ms, paused_since, paused_until, polled_at, created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			append([]any{id}, args...)...)
		w.ID = id
		return err
	}
	_, err = x.t.Exec(`UPDATE watches SET repo=?, identity=?, forge_account=?, tracker=?, tracker_identity=?, tracker_account=?,
		labels=?, marker=?, interval_ms=?, paused_since=?, paused_until=?, polled_at=?, created_at=? WHERE id=?`, append(args, w.ID)...)
	return err
}

func trackerText(t domain.Tracker) string {
	if t.Kind == "" {
		return ""
	}
	return t.String()
}

// DeleteWatch forgets a watch: its registration, snapshots, problems and
// undelivered changes. Delivered and ignored changes stay in the ledger.
func (x *tx) DeleteWatch(id int64) error {
	if _, err := x.t.Exec(`DELETE FROM changes WHERE watch_id=? AND status IN ('pending','held')`, id); err != nil {
		return err
	}
	if _, err := x.t.Exec(`DELETE FROM leases WHERE watch_id=?`, id); err != nil {
		return err
	}
	_, err := x.t.Exec(`DELETE FROM watches WHERE id=?`, id)
	return err
}

// ---- registrations

const regCols = `watch_id, harness, session, pid, pid_start, registered_at, heard_at, ended_at, end_reason, replaced`

func scanReg(r scanner) (*application.Registration, error) {
	var g application.Registration
	var registered, heard, ended sql.NullString
	if err := r.Scan(&g.WatchID, &g.Harness, &g.Session, &g.Process.PID, &g.Process.Start, &registered, &heard, &ended,
		&g.EndReason, &g.Replaced); err != nil {
		return nil, err
	}
	g.RegisteredAt, g.HeardAt, g.EndedAt = parseTS(registered), parseTS(heard), parseTSP(ended)
	return &g, nil
}

func (x *tx) Registration(watchID int64) (*application.Registration, error) {
	return noRow(scanReg(x.t.QueryRow(`SELECT `+regCols+` FROM registrations WHERE watch_id=?`, watchID)))
}

func (x *tx) RegistrationBySession(session string) (*application.Registration, error) {
	return noRow(scanReg(x.t.QueryRow(`SELECT `+regCols+` FROM registrations WHERE session=?
		ORDER BY registered_at DESC LIMIT 1`, session)))
}

func (x *tx) RegistrationReplacing(session string) (*application.Registration, error) {
	return noRow(scanReg(x.t.QueryRow(`SELECT `+regCols+` FROM registrations WHERE replaced=?
		ORDER BY registered_at DESC LIMIT 1`, session)))
}

func (x *tx) SaveRegistration(g application.Registration) error {
	_, err := x.t.Exec(`INSERT INTO registrations(`+regCols+`) VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(watch_id) DO UPDATE SET harness=excluded.harness, session=excluded.session, pid=excluded.pid,
		pid_start=excluded.pid_start, registered_at=excluded.registered_at, heard_at=excluded.heard_at,
		ended_at=excluded.ended_at, end_reason=excluded.end_reason, replaced=excluded.replaced`,
		g.WatchID, g.Harness, g.Session, g.Process.PID, g.Process.Start, ts(g.RegisteredAt), ts(g.HeardAt), tsp(g.EndedAt),
		g.EndReason, g.Replaced)
	return err
}

// ---- adapters

func scanAdapter(r scanner) (*application.Adapter, error) {
	var a application.Adapter
	var hello, heard sql.NullString
	if err := r.Scan(&a.Session, &a.Process.PID, &a.Process.Start, &hello, &heard, &a.Protocol); err != nil {
		return nil, err
	}
	a.HelloAt, a.HeardAt = parseTS(hello), parseTS(heard)
	return &a, nil
}

func (x *tx) Adapter(session string) (*application.Adapter, error) {
	return noRow(scanAdapter(x.t.QueryRow(`SELECT session, pid, pid_start, hello_at, heard_at, protocol FROM adapters WHERE session=?`, session)))
}

func (x *tx) AdaptersOf(p application.Process) ([]application.Adapter, error) {
	rows, err := x.t.Query(`SELECT session, pid, pid_start, hello_at, heard_at, protocol FROM adapters WHERE pid=? AND pid_start=?`, p.PID, p.Start)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []application.Adapter
	for rows.Next() {
		a, err := scanAdapter(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

func (x *tx) SaveAdapter(a application.Adapter) error {
	_, err := x.t.Exec(`INSERT INTO adapters(session, pid, pid_start, hello_at, heard_at, protocol) VALUES(?,?,?,?,?,?)
		ON CONFLICT(session) DO UPDATE SET pid=excluded.pid, pid_start=excluded.pid_start, hello_at=excluded.hello_at,
		heard_at=excluded.heard_at, protocol=excluded.protocol`, a.Session, a.Process.PID, a.Process.Start, ts(a.HelloAt), ts(a.HeardAt), a.Protocol)
	return err
}

func (x *tx) DeleteAdapter(session string) error {
	_, err := x.t.Exec(`DELETE FROM adapters WHERE session=?`, session)
	return err
}

// ---- snapshots

func (x *tx) Snapshots(watchID int64, source application.SourceName) (map[string]domain.Snapshot, error) {
	rows, err := x.t.Query(`SELECT ref, data FROM snapshots WHERE watch_id=? AND source=?`, watchID, string(source))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]domain.Snapshot{}
	for rows.Next() {
		var ref, data string
		if err := rows.Scan(&ref, &data); err != nil {
			return nil, err
		}
		var s domain.Snapshot
		if err := json.Unmarshal([]byte(data), &s); err != nil {
			return nil, err
		}
		out[ref] = s
	}
	return out, rows.Err()
}

func (x *tx) SaveSnapshot(watchID int64, source application.SourceName, s domain.Snapshot) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = x.t.Exec(`INSERT INTO snapshots(watch_id, source, ref, data) VALUES(?,?,?,?)
		ON CONFLICT(watch_id, source, ref) DO UPDATE SET data=excluded.data`, watchID, string(source), s.Item.Ref, string(data))
	return err
}

func (x *tx) DeleteSnapshot(watchID int64, source application.SourceName, ref string) error {
	_, err := x.t.Exec(`DELETE FROM snapshots WHERE watch_id=? AND source=? AND ref=?`, watchID, string(source), ref)
	return err
}

// ---- changes

func (x *tx) AddChange(c application.StoredChange) (int64, error) {
	data, err := json.Marshal(c.Change)
	if err != nil {
		return 0, err
	}
	var repo string
	if err := x.t.QueryRow(`SELECT repo FROM watches WHERE id=?`, c.WatchID).Scan(&repo); err != nil {
		return 0, err
	}
	res, err := x.t.Exec(`INSERT INTO changes(watch_id, repo, source, item, kind, at, found_at, status, reason, batch_id, data)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`, c.WatchID, repo, string(c.Source), c.Change.Item, string(c.Change.Kind),
		ts(c.Change.At), ts(c.FoundAt), string(c.Change.Status), c.Change.Reason, c.Batch, string(data))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (x *tx) Changes(q application.ChangeQuery) ([]application.StoredChange, error) {
	var where []string
	var args []any
	if q.WatchID != 0 {
		where, args = append(where, "watch_id=?"), append(args, q.WatchID)
	}
	if len(q.Status) > 0 {
		marks := strings.TrimSuffix(strings.Repeat("?,", len(q.Status)), ",")
		where = append(where, "status IN ("+marks+")")
		for _, s := range q.Status {
			args = append(args, string(s))
		}
	}
	if !q.Since.IsZero() {
		where, args = append(where, "found_at>=?"), append(args, ts(q.Since))
	}
	if q.Item != "" {
		where, args = append(where, "item=?"), append(args, q.Item)
	}
	if q.Batch != 0 {
		where, args = append(where, "batch_id=?"), append(args, q.Batch)
	}
	if q.IDs != nil {
		if len(q.IDs) == 0 {
			return nil, nil
		}
		marks := strings.TrimSuffix(strings.Repeat("?,", len(q.IDs)), ",")
		where = append(where, "id IN ("+marks+")")
		for _, id := range q.IDs {
			args = append(args, id)
		}
	}
	query := `SELECT id, watch_id, source, found_at, status, reason, batch_id, data FROM changes`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	rows, err := x.t.Query(query+" ORDER BY id", args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []application.StoredChange
	for rows.Next() {
		var c application.StoredChange
		var source, status, reason, data string
		var found sql.NullString
		if err := rows.Scan(&c.ID, &c.WatchID, &source, &found, &status, &reason, &c.Batch, &data); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(data), &c.Change); err != nil {
			return nil, err
		}
		c.Source, c.FoundAt = application.SourceName(source), parseTS(found)
		c.Change.Status, c.Change.Reason = domain.Status(status), reason
		out = append(out, c)
	}
	return out, rows.Err()
}

func (x *tx) SetChangeStatus(ids []int64, status domain.Status, batch int64) error {
	for _, id := range ids {
		if _, err := x.t.Exec(`UPDATE changes SET status=?, batch_id=CASE WHEN ?=0 THEN batch_id ELSE ? END WHERE id=?`,
			string(status), batch, batch, id); err != nil {
			return err
		}
	}
	return nil
}

// ---- batches

func (x *tx) batchChanges(b *application.Batch) error {
	rows, err := x.t.Query(`SELECT change_id FROM batch_changes WHERE batch_id=? ORDER BY change_id`, b.ID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	b.Changes = []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
		b.Changes = append(b.Changes, id)
	}
	return rows.Err()
}

func (x *tx) scanBatch(r *sql.Row) (*application.Batch, error) {
	var b application.Batch
	var offered, acked sql.NullString
	if err := r.Scan(&b.ID, &b.WatchID, &offered, &acked); err != nil {
		return noRow(&b, err)
	}
	b.OfferedAt, b.AckedAt = parseTS(offered), parseTSP(acked)
	return &b, x.batchChanges(&b)
}

func (x *tx) OpenBatch(watchID int64) (*application.Batch, error) {
	return x.scanBatch(x.t.QueryRow(`SELECT id, watch_id, offered_at, acked_at FROM batches
		WHERE watch_id=? AND acked_at IS NULL ORDER BY id DESC LIMIT 1`, watchID))
}

func (x *tx) Batch(id int64) (*application.Batch, error) {
	return x.scanBatch(x.t.QueryRow(`SELECT id, watch_id, offered_at, acked_at FROM batches WHERE id=?`, id))
}

func (x *tx) AddBatch(b *application.Batch) error {
	id, err := x.nextID("batch", "batches")
	if err != nil {
		return err
	}
	if _, err := x.t.Exec(`INSERT INTO batches(id, watch_id, offered_at, acked_at) VALUES(?,?,?,?)`,
		id, b.WatchID, ts(b.OfferedAt), tsp(b.AckedAt)); err != nil {
		return err
	}
	b.ID = id
	for _, c := range b.Changes {
		if _, err := x.t.Exec(`INSERT INTO batch_changes(batch_id, change_id) VALUES(?,?)`, b.ID, c); err != nil {
			return err
		}
	}
	return nil
}

func (x *tx) SaveBatch(b application.Batch) error {
	_, err := x.t.Exec(`UPDATE batches SET offered_at=?, acked_at=? WHERE id=?`, ts(b.OfferedAt), tsp(b.AckedAt), b.ID)
	return err
}

// ---- problems

func (x *tx) OpenProblems(watchID int64) ([]application.Problem, error) {
	rows, err := x.t.Query(`SELECT id, watch_id, source, code, text, hint, since, delivered_at, cleared_at FROM problems
		WHERE watch_id=? AND cleared_at IS NULL ORDER BY id`, watchID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []application.Problem
	for rows.Next() {
		var p application.Problem
		var source string
		var since, delivered, cleared sql.NullString
		if err := rows.Scan(&p.ID, &p.WatchID, &source, &p.Code, &p.Text, &p.Hint, &since, &delivered, &cleared); err != nil {
			return nil, err
		}
		p.Source, p.Since, p.DeliveredAt, p.ClearedAt = application.SourceName(source), parseTS(since), parseTSP(delivered), parseTSP(cleared)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (x *tx) AddProblem(p *application.Problem) error {
	res, err := x.t.Exec(`INSERT INTO problems(watch_id, source, code, text, hint, since, delivered_at, cleared_at) VALUES(?,?,?,?,?,?,?,?)`,
		p.WatchID, string(p.Source), p.Code, p.Text, p.Hint, ts(p.Since), tsp(p.DeliveredAt), tsp(p.ClearedAt))
	if err != nil {
		return err
	}
	p.ID, err = res.LastInsertId()
	return err
}

func (x *tx) SaveProblem(p application.Problem) error {
	_, err := x.t.Exec(`UPDATE problems SET delivered_at=?, cleared_at=?, text=? WHERE id=?`,
		tsp(p.DeliveredAt), tsp(p.ClearedAt), p.Text, p.ID)
	return err
}

// ---- source state

func (x *tx) SourceState(watchID int64, source application.SourceName) (application.SourceState, error) {
	s := application.SourceState{WatchID: watchID, Source: source}
	var rate string
	var skip, ok sql.NullString
	err := x.t.QueryRow(`SELECT data, failures, rate, skip_until, ok_at FROM source_state WHERE watch_id=? AND source=?`,
		watchID, string(source)).Scan(&s.Data, &s.Failures, &rate, &skip, &ok)
	if errors.Is(err, sql.ErrNoRows) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal([]byte(rate), &s.Rate); err != nil {
		return s, err
	}
	s.SkipUntil, s.OKAt = parseTS(skip), parseTS(ok)
	return s, nil
}

func (x *tx) SaveSourceState(s application.SourceState) error {
	rate, err := json.Marshal(s.Rate)
	if err != nil {
		return err
	}
	var skip, ok any
	if !s.SkipUntil.IsZero() {
		skip = ts(s.SkipUntil)
	}
	if !s.OKAt.IsZero() {
		ok = ts(s.OKAt)
	}
	_, err = x.t.Exec(`INSERT INTO source_state(watch_id, source, data, failures, rate, skip_until, ok_at) VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(watch_id, source) DO UPDATE SET data=excluded.data, failures=excluded.failures, rate=excluded.rate,
		skip_until=excluded.skip_until, ok_at=excluded.ok_at`, s.WatchID, string(s.Source), s.Data, s.Failures, string(rate), skip, ok)
	return err
}

// ---- leases

func (x *tx) TakeLease(watchID int64, holder application.Process, until time.Time, free func(application.Lease) bool) (bool, error) {
	var l application.Lease
	var expires sql.NullString
	err := x.t.QueryRow(`SELECT watch_id, pid, pid_start, expires_at FROM leases WHERE watch_id=?`, watchID).
		Scan(&l.WatchID, &l.Process.PID, &l.Process.Start, &expires)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return false, err
	default:
		l.ExpiresAt = parseTS(expires)
		if l.Process != holder && !free(l) {
			return false, nil
		}
	}
	_, err = x.t.Exec(`INSERT INTO leases(watch_id, pid, pid_start, expires_at) VALUES(?,?,?,?)
		ON CONFLICT(watch_id) DO UPDATE SET pid=excluded.pid, pid_start=excluded.pid_start, expires_at=excluded.expires_at`,
		watchID, holder.PID, holder.Start, ts(until))
	return err == nil, err
}

func (x *tx) ReleaseLease(watchID int64, holder application.Process) error {
	_, err := x.t.Exec(`DELETE FROM leases WHERE watch_id=? AND pid=? AND pid_start=?`, watchID, holder.PID, holder.Start)
	return err
}

// ---- pruning

func (x *tx) PrunedAt() (time.Time, error) {
	var s sql.NullString
	err := x.t.QueryRow(`SELECT value FROM meta WHERE key='pruned_at'`).Scan(&s)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	return parseTS(s), err
}

func (x *tx) count(query string, args ...any) (int, error) {
	var n int
	err := x.t.QueryRow(query, args...).Scan(&n)
	return n, err
}

// Prune removes what state.md's pruning table lists.
func (x *tx) Prune(p application.PrunePlan, dryRun bool) (application.PruneCounts, error) {
	var c application.PruneCounts
	before := ts(p.Before)
	var err error
	if c.Changes, err = x.count(`SELECT count(*) FROM changes WHERE status IN ('delivered','ignored') AND found_at<?`, before); err != nil {
		return c, err
	}
	if c.Batches, err = x.count(`SELECT count(*) FROM batches WHERE acked_at IS NOT NULL AND acked_at<?`, before); err != nil {
		return c, err
	}
	if c.Problems, err = x.count(`SELECT count(*) FROM problems WHERE cleared_at IS NOT NULL AND cleared_at<?`, before); err != nil {
		return c, err
	}
	rows, err := x.t.Query(`SELECT ` + regCols + ` FROM registrations`)
	if err != nil {
		return c, err
	}
	var stale []int64
	for rows.Next() {
		g, err := scanReg(rows)
		if err != nil {
			_ = rows.Close()
			return c, err
		}
		last := g.HeardAt
		if g.EndedAt != nil {
			last = *g.EndedAt
		}
		if last.Before(p.Before) {
			stale = append(stale, g.WatchID)
		}
	}
	_ = rows.Close()
	for _, id := range stale {
		g, err := x.Registration(id)
		if err != nil {
			return c, err
		}
		if g != nil && p.Dormant(*g) {
			c.Registrations++
			c.Watches++
			if !dryRun {
				if err := x.DeleteWatch(id); err != nil {
					return c, err
				}
			}
		}
	}
	if dryRun {
		return c, nil
	}
	for _, q := range []string{
		`DELETE FROM changes WHERE status IN ('delivered','ignored') AND found_at<?`,
		`DELETE FROM batches WHERE acked_at IS NOT NULL AND acked_at<?`,
		`DELETE FROM problems WHERE cleared_at IS NOT NULL AND cleared_at<?`,
	} {
		if _, err := x.t.Exec(q, before); err != nil {
			return c, err
		}
	}
	if _, err := x.t.Exec(`INSERT INTO meta(key, value) VALUES('pruned_at', ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		ts(p.Now)); err != nil {
		return c, err
	}
	_, err = x.t.Exec(`PRAGMA incremental_vacuum`)
	return c, err
}

// JournalMode answers SQLite's journal mode, for doctor and tests.
func (s *Store) JournalMode(ctx context.Context) (string, error) {
	var m string
	err := s.db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&m)
	return m, err
}
