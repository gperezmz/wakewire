package sqlitestore_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/gperezmz/wakewire/v2/internal/watch/adapters/sqlitestore"
	"github.com/gperezmz/wakewire/v2/internal/watch/application"
	"github.com/gperezmz/wakewire/v2/internal/watch/application/storetest"
)

func open(t *testing.T) application.Store {
	s, err := sqlitestore.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestStateDBKeepsTheContract(t *testing.T) { storetest.Run(t, open) }

func TestStateDBIsPrivateAndInWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := sqlitestore.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("%v %v", st.Mode(), err)
	}
	mode, err := s.JournalMode(context.Background())
	if err != nil || mode != "wal" {
		t.Fatalf("%q %v", mode, err)
	}
}
