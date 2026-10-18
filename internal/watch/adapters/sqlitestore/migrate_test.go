package sqlitestore

import (
	"context"
	"database/sql"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/gperezmz/wakewire/v2/internal/watch/application"
	"github.com/gperezmz/wakewire/v2/internal/watch/domain"
)

// A state.db made by an earlier binary migrates in place, and the ids its
// deleted watches and pruned batches held are not given out again.
func TestMigratingAnOlderDatabaseKeepsDeletedIDsRetired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for v := range 2 {
		if _, err := raw.Exec(migrations[v]); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{
		`INSERT INTO meta(key, value) VALUES('schema_version', '2')`,
		// Watch 7 was deleted; its delivered change stayed in the ledger.
		`INSERT INTO changes(id, watch_id, repo, source, item, kind, at, found_at, status, batch_id, data)
		 VALUES(1, 7, 'github.com/o/x', 'forge', '#1', 'closed', '2026-10-16T09:00:00Z', '2026-10-16T09:00:00Z', 'delivered', 4, '{}')`,
		`INSERT INTO batches(id, watch_id, offered_at) VALUES(3, 2, '2026-10-16T09:00:00Z')`,
	} {
		if _, err := raw.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	repo, _ := domain.ParseRepo("github.com/o/x")
	w := application.Watch{Repo: repo, Identity: "jdoe", CreatedAt: time.Now()}
	b := &application.Batch{OfferedAt: time.Now()}
	err = s.Tx(ctx, func(tx application.Tx) error {
		if err := tx.SaveWatch(&w); err != nil {
			return err
		}
		b.WatchID = w.ID
		return tx.AddBatch(b)
	})
	if err != nil {
		t.Fatal(err)
	}
	if w.ID <= 7 {
		t.Errorf("the new watch took id %d, which a deleted watch held", w.ID)
	}
	if b.ID <= 4 {
		t.Errorf("the new batch took id %d, which an earlier batch held", b.ID)
	}
	var v string
	if err := s.db.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&v); err != nil || v != strconv.Itoa(schemaVersion) {
		t.Fatalf("schema version %q %v", v, err)
	}
}
