package sqlitestore

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/gperezmz/wakewire/internal/watch/application"
	"github.com/gperezmz/wakewire/internal/watch/domain"
)

// olderDB writes a state.db as the release before the latest schema made it:
// at version 2, with a change in the ledger.
func olderDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	for v := range 2 {
		if _, err := raw.Exec(migrations[v]); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{
		`INSERT INTO meta(key, value) VALUES('schema_version', '2')`,
		`INSERT INTO changes(id, watch_id, repo, source, item, kind, at, found_at, status, batch_id, data)
		 VALUES(1, 7, 'github.com/o/x', 'forge', '#1', 'closed', '2026-10-16T09:00:00Z', '2026-10-16T09:00:00Z', 'delivered', 4, '{"item":"#1","url":"","kind":"closed","at":"2026-10-16T09:00:00Z"}')`,
	} {
		if _, err := raw.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// scalar reads one value from a state.db with a plain connection.
func scalar(t *testing.T, path, query string) string {
	t.Helper()
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	var v string
	if err := raw.QueryRow(query).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// A dry store over a state.db of an older schema reads as the migrated one
// would, and leaves the file at the version it had: not even a migration is kept.
func TestADryStoreReadsAnOlderSchemaAndKeepsItsVersion(t *testing.T) {
	ctx := context.Background()
	path := olderDB(t)
	s, err := OpenDry(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	err = s.Tx(ctx, func(tx application.Tx) error {
		got, err := tx.Changes(application.ChangeQuery{WatchID: 7})
		if err != nil {
			return err
		}
		if len(got) != 1 || got[0].Change.Item != "#1" || got[0].Change.Status != domain.StatusDelivered {
			t.Errorf("the older ledger does not read: %+v", got)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// Reading again, in a transaction of its own, still sees a migrated schema.
	if err := s.Tx(ctx, func(tx application.Tx) error { _, err := tx.Watches(); return err }); err != nil {
		t.Fatalf("the second read does not see the migrated schema: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if v := scalar(t, path, `SELECT value FROM meta WHERE key='schema_version'`); v != "2" {
		t.Fatalf("the dry store left the schema at version %s", v)
	}
	// A real store opened after it migrates as ever.
	real, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	_ = real.Close()
	if v := scalar(t, path, `SELECT value FROM meta WHERE key='schema_version'`); v != strconv.Itoa(schemaVersion) {
		t.Fatalf("a real open did not migrate: version %s", v)
	}
}

// A write through a dry store is read back by nobody: the transaction that
// made it is rolled back, however it ended.
func TestADryStoreKeepsNoWrite(t *testing.T) {
	ctx := context.Background()
	for name, path := range map[string]string{
		"an older schema":    olderDB(t),
		"the current schema": currentDB(t),
	} {
		t.Run(name, func(t *testing.T) {
			s, err := OpenDry(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			repo, _ := domain.ParseRepo("github.com/o/dry")
			w := application.Watch{Repo: repo, Identity: "jdoe", CreatedAt: time.Now()}
			err = s.Tx(ctx, func(tx application.Tx) error {
				if err := tx.SaveWatch(&w); err != nil {
					return err
				}
				// Inside the transaction the write is there to be read.
				all, err := tx.Watches()
				if err != nil || len(all) != 1 {
					t.Errorf("the write is not visible to its own transaction: %v %v", all, err)
				}
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			err = s.Tx(ctx, func(tx application.Tx) error {
				all, err := tx.Watches()
				if len(all) != 0 {
					t.Errorf("a dry write was kept: %+v", all)
				}
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			_ = s.Close()
			real, err := Open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = real.Close() }()
			_ = real.Tx(ctx, func(tx application.Tx) error {
				if all, _ := tx.Watches(); len(all) != 0 {
					t.Errorf("the file holds a dry write: %+v", all)
				}
				return nil
			})
		})
	}
}

// currentDB is a state.db at the current schema, empty.
func currentDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// A state.db written by a newer release is refused under a code of its own,
// by a real store and a dry one alike, and left as it was.
func TestAStateDBNewerThanTheBinaryIsStateNewer(t *testing.T) {
	ctx := context.Background()
	path := currentDB(t)
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`UPDATE meta SET value=? WHERE key='schema_version'`, strconv.Itoa(schemaVersion+1)); err != nil {
		t.Fatal(err)
	}
	_ = raw.Close()
	for name, open := range map[string]func(context.Context, string) (*Store, error){"Open": Open, "OpenDry": OpenDry} {
		_, err := open(ctx, path)
		var c interface {
			Code() string
			Hint() string
		}
		if !errors.As(err, &c) || c.Code() != "state_newer" || c.Hint() == "" {
			t.Errorf("%s: %v", name, err)
		}
	}
	if v := scalar(t, path, `SELECT value FROM meta WHERE key='schema_version'`); v != strconv.Itoa(schemaVersion+1) {
		t.Fatalf("the newer state.db was changed: version %s", v)
	}
}
