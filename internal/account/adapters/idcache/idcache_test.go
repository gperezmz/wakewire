package idcache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gperezmz/wakewire/v2/internal/account/application"
)

func TestGetPutForget(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache")
	c := New(dir)
	id := application.Identity{Login: "me", ID: "7", Name: "Me"}
	if _, ok := c.Get("work", "fp1"); ok {
		t.Fatal("a cold cache answered")
	}
	if err := c.Put("work", "fp1", id); err != nil {
		t.Fatal(err)
	}
	if got, ok := c.Get("work", "fp1"); !ok || got != id {
		t.Fatalf("%+v %v", got, ok)
	}
	if _, ok := c.Get("work", "fp2"); ok {
		t.Fatal("another token's fingerprint hit")
	}
	if _, ok := c.Get("other", "fp1"); ok {
		t.Fatal("another account hit")
	}
	if err := c.Put("work", "fp2", application.Identity{Login: "new"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get("work", "fp1"); ok {
		t.Fatal("the old token's answer was kept")
	}
	if err := c.Forget("work"); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Get("work", "fp2"); ok {
		t.Fatal("forgotten but present")
	}
}

func TestFilesAreNamedByHashesAndKeepNoSecret(t *testing.T) {
	dir := t.TempDir()
	c := New(dir)
	if err := c.Put("work", "the-fingerprint", application.Identity{Login: "me"}); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("%v", entries)
	}
	name := entries[0].Name()
	if strings.Contains(name, "work") || strings.Contains(name, "fingerprint") {
		t.Fatalf("file name %q shows its inputs", name)
	}
	info, _ := entries[0].Info()
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode())
	}
}
