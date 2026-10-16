package proc

import (
	"os"
	"os/exec"
	"testing"
)

func TestOwnProcessRunsWithAStableStart(t *testing.T) {
	a, ok, err := Table{}.StartOf(os.Getpid())
	if err != nil || !ok || a == "" {
		t.Fatalf("%q %v %v", a, ok, err)
	}
	b, _, _ := Table{}.StartOf(os.Getpid())
	if a != b {
		t.Fatalf("start time moved: %s %s", a, b)
	}
}

func TestAnEndedProcessIsGone(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := (Table{}).StartOf(cmd.Process.Pid); ok || err != nil {
		t.Fatalf("an ended process runs: %v", err)
	}
	if _, ok, _ := (Table{}).StartOf(0); ok {
		t.Fatal("pid 0 runs")
	}
}
