package cli

import (
	"testing"

	"github.com/gperezmz/wakewire/internal/watch/application"
)

func TestRegisterJSONAlwaysSaysWhetherItWasADryRun(t *testing.T) {
	for _, dry := range []bool{false, true} {
		w := newFakeWorld()
		w.env["CLAUDE_CODE_SESSION_ID"] = "s1"
		w.watch.registerRes = application.RegisterResult{Outcome: "registered", DryRun: dry}
		args := []string{"register", "--json"}
		if dry {
			args = append(args, "--dry-run")
		}
		code, out, _ := w.run(t, args...)
		if code != 0 {
			t.Fatalf("exit %d\n%s", code, out)
		}
		got, present := object(t, out)["dryRun"]
		if !present || got != dry {
			t.Errorf("dryRun %v (present %v), want %v:\n%s", got, present, dry, out)
		}
	}
}
