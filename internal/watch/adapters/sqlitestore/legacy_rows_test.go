package sqlitestore

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gperezmz/wakewire/internal/watch/application"
	"github.com/gperezmz/wakewire/internal/watch/application/fakes"
	"github.com/gperezmz/wakewire/internal/watch/domain"
)

// Rows written by v1.0.3 carry "truncated" in their JSON, true on a body cut
// at 4000 characters and false on every other. Schema step 4 strips the false,
// which says what absence says, and keeps the true, which marks a body as cut;
// it runs once, and a dry open runs it without keeping it.
func TestSchemaStep4StripsAStoredTruncatedFalseAndKeepsATrue(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	repo, _ := domain.ParseRepo("github.com/o/x")
	w := application.Watch{Repo: repo, Identity: "jdoe", CreatedAt: time.Now()}
	b := &application.Batch{OfferedAt: time.Now()}
	var ids []int64
	err = s.Tx(ctx, func(tx application.Tx) error {
		if err := tx.SaveWatch(&w); err != nil {
			return err
		}
		for _, item := range []string{"#1", "#2", "#3"} {
			id, err := tx.AddChange(application.StoredChange{WatchID: w.ID, Source: application.SourceForge, FoundAt: time.Now(),
				Change: domain.Change{Item: item, Kind: domain.KindComment, At: time.Now(), Status: domain.StatusPending}.WithBody("body of " + item)})
			if err != nil {
				return err
			}
			ids = append(ids, id)
		}
		b.WatchID, b.Changes = w.ID, ids
		return tx.AddBatch(b)
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// Make the file what v1.0.3 left: schema 3, the first row cut, the second
	// marked false, the third as written now.
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	for i, data := range []string{
		`{"item":"#1","url":"","kind":"comment","at":"2026-10-16T09:00:00Z","body":"body of #1","truncated":true}`,
		`{"item":"#2","url":"","kind":"comment","at":"2026-10-16T09:00:00Z","body":"body of #2","truncated":false}`,
	} {
		if _, err := raw.Exec(`UPDATE changes SET data=? WHERE id=?`, data, ids[i]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := raw.Exec(`UPDATE meta SET value='3' WHERE key='schema_version'`); err != nil {
		t.Fatal(err)
	}
	stored := func() (version string, data []string) {
		t.Helper()
		if err := raw.QueryRow(`SELECT value FROM meta WHERE key='schema_version'`).Scan(&version); err != nil {
			t.Fatal(err)
		}
		for _, id := range ids {
			var d string
			if err := raw.QueryRow(`SELECT data FROM changes WHERE id=?`, id).Scan(&d); err != nil {
				t.Fatal(err)
			}
			data = append(data, d)
		}
		return version, data
	}
	_, before := stored()

	// A dry open migrates inside its transaction and keeps nothing.
	dry, err := OpenDry(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	_ = dry.Close()
	if v, data := stored(); v != "3" || !slices.Equal(data, before) {
		t.Fatalf("a dry open kept the migration: version %s\n%v", v, data)
	}

	// A real open runs step 4, and a second open has nothing left to do.
	for range 2 {
		s, err = Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		_ = s.Close()
		v, data := stored()
		if v != "4" || !strings.Contains(data[0], `"truncated":true`) || strings.Contains(data[1], "truncated") ||
			strings.Contains(data[2], "truncated") || !strings.Contains(data[1], `"body":"body of #2"`) {
			t.Fatalf("after step 4: version %s\n%s", v, strings.Join(data, "\n"))
		}
	}

	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	svc := application.New(s, &fakes.Sources{}, &fakes.Accounts{}, &fakes.Repos{}, &fakes.Processes{}, &fakes.Clock{T: time.Now()}, application.Settings{})
	view, err := svc.Changes(ctx, b.ID)
	if err != nil || len(view.Changes) != 3 {
		t.Fatalf("%+v %v", view, err)
	}
	if got := view.Changes[0].Truncated; got == nil || !*got {
		t.Errorf("a body stored cut lost its mark: %v", got)
	}
	for i, c := range view.Changes[1:] {
		if c.Truncated != nil {
			t.Errorf("change %d keeps a truncated: %v", i+2, *c.Truncated)
		}
	}
}
