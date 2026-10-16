package home

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePrefersWakewireHome(t *testing.T) {
	dir := t.TempDir()
	p, err := Resolve(func(k string) string {
		return map[string]string{"WAKEWIRE_HOME": dir, "HOME": "/nope"}[k]
	})
	if err != nil || p.State != filepath.Join(dir, "state.db") {
		t.Fatalf("%v %v", p, err)
	}
}

func TestEnsureMakesPrivateFolders(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ww")
	p, _ := Resolve(func(k string) string { return map[string]string{"WAKEWIRE_HOME": dir}[k] })
	if err := p.Ensure(); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{p.Home, p.Cache} {
		st, err := os.Stat(d)
		if err != nil || st.Mode().Perm() != 0o700 {
			t.Fatalf("%s: %v %v", d, st.Mode(), err)
		}
	}
}
