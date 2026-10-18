package cli

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gperezmz/wakewire/internal/watch/application"
	"github.com/gperezmz/wakewire/internal/watch/domain"
)

// threeChanges is a ledger of three changes, the oldest first as the store
// answers them.
func threeChanges() *application.LedgerResult {
	at := time.Now().Add(-time.Hour)
	entry := func(id int64, item string, status domain.Status) application.LedgerEntry {
		return application.LedgerEntry{ID: id, Source: application.SourceForge, Item: item, Kind: domain.Kind("comment"),
			By: "alice", At: at, FoundAt: at, Status: status, Reason: "reason " + item, Batch: id * 10, Label: "ready", Code: "c" + item}
	}
	return &application.LedgerResult{Watch: "github.com/o/x", Changes: []application.LedgerEntry{
		entry(1, "#1", domain.Status("delivered")), entry(2, "#2", domain.Status("ignored")), entry(3, "#3", domain.Status("pending"))}}
}

func TestLedgerShowsTheMostRecentLimitAndHowManyThereAre(t *testing.T) {
	w := newFakeWorld()
	w.watch.ledger = threeChanges()
	code, out, _ := w.run(t, "ledger", "--limit", "2")
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, want := range []string{"watch: github.com/o/x", "count: 2 of 3 total", "changes[2]{found,item,kind,status,batch}:",
		`"#2",comment,ignored,20`, `"#3",comment,pending,30`,
		// The hint is the command as typed, less --limit, plus --limit 0.
		"Run `wakewire ledger --limit 0` for all 3 changes",
		"Run `wakewire changes <batch>` with a row's batch for what it delivered"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, `"#1"`) {
		t.Errorf("the oldest change is shown with --limit 2:\n%s", out)
	}
}

func TestLedgerLimitZeroShowsEveryChangeAndTheDefaultShowsAll100(t *testing.T) {
	w := newFakeWorld()
	w.watch.ledger = threeChanges()
	_, out, _ := w.run(t, "ledger", "--limit", "0")
	if !strings.Contains(out, "count: 3 of 3 total") || !strings.Contains(out, "changes[3]{") || strings.Contains(out, "--limit 0` for all") {
		t.Errorf("--limit 0:\n%s", out)
	}
	// The default is 100: 101 changes show the last 100.
	many := &application.LedgerResult{Watch: "github.com/o/x"}
	for i := 1; i <= 101; i++ {
		many.Changes = append(many.Changes, application.LedgerEntry{ID: int64(i), Item: fmt.Sprintf("#%d", i), FoundAt: time.Now(), Kind: "comment", Status: "delivered"})
	}
	w.watch.ledger = many
	_, out, _ = w.run(t, "ledger")
	if !strings.Contains(out, "count: 100 of 101 total") || strings.Contains(out, `"#1",`) {
		t.Errorf("default limit:\n%s", out)
	}
	if !strings.Contains(out, `"#101",comment`) {
		t.Errorf("the most recent change is missing:\n%s", out)
	}
}

func TestLedgerFieldsAddColumnsAndANegativeLimitIsInvalid(t *testing.T) {
	w := newFakeWorld()
	w.watch.ledger = threeChanges()
	code, out, _ := w.run(t, "ledger", "--fields", "by,reason,label,code,source", "--limit", "1")
	if code != exitOK {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, want := range []string{"changes[1]{found,item,kind,status,batch,by,reason,label,code,source}:",
		`"#3",comment,pending,30,alice,reason #3,ready,c#3,forge`} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	code, out, _ = w.run(t, "ledger", "--limit", "-1", "--json")
	if code != exitUsage || errorCode(t, out) != "invalid_value" {
		t.Errorf("a negative --limit: exit %d\n%s", code, out)
	}
}

// A hint is the command as it was typed, so it acts on the same watch and
// the same window from any folder, and keeps every flag but the one it changes.
func TestLedgerHintsAreTheTypedCommandLessTheFlagTheyChange(t *testing.T) {
	w := newFakeWorld()
	w.watch.ledger = threeChanges()
	_, out, _ := w.run(t, "ledger", "o/x", "--limit", "1", "--identity", "jdoe", "--since=2h", "--item", "PR #41", "--fields", "by")
	for _, want := range []string{
		"Run `wakewire ledger o/x --identity jdoe --since=2h --item 'PR #41' --fields by --limit 0` for all 3 changes",
		"Run `wakewire ledger o/x --limit 1 --identity jdoe --since=2h --item 'PR #41' --fields by,reason` for who made each change and why it was ignored",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	// The fields hint drops the --fields typed and gives its own.
	if strings.Contains(out, "--fields by --fields") || strings.Contains(out, "--fields by,reason --fields") {
		t.Errorf("a flag is given twice in a hint:\n%s", out)
	}
	// --limit given as --limit=N is dropped whole, and a limit that shows everything has no --limit 0 hint.
	_, out, _ = w.run(t, "ledger", "--limit=2")
	if !strings.Contains(out, "Run `wakewire ledger --limit 0` for all 3 changes") {
		t.Errorf("--limit=2:\n%s", out)
	}

	w.watch.ledger = &application.LedgerResult{Watch: "github.com/o/x", Changes: []application.LedgerEntry{}}
	_, out, _ = w.run(t, "ledger", "--identity", "jdoe", "--since", "2h", "--item", "#9")
	for _, want := range []string{"Run `wakewire ledger --identity jdoe` for every change stored",
		"`wakewire status github.com/o/x --identity jdoe`"} {
		if !strings.Contains(out, want) {
			t.Errorf("empty ledger: stdout lacks %q:\n%s", want, out)
		}
	}
	// --item alone also offers to widen; with neither there is nothing to widen.
	_, out, _ = w.run(t, "ledger", "--item", "#9")
	if !strings.Contains(out, "Run `wakewire ledger` for every change stored") {
		t.Errorf("--item alone:\n%s", out)
	}
	code, out, errOut := w.run(t, "ledger")
	if code != exitOK || errOut != "" || !strings.Contains(out, "changes: 0 changes stored for this watch that match") ||
		!strings.Contains(out, "`wakewire status github.com/o/x`") || strings.Contains(out, "for every change stored") {
		t.Errorf("nothing to widen: exit %d\n%s", code, out)
	}
}

// The batch column is empty, as "", for a change no batch carries yet, so
// every row keeps its columns. --json keeps what it always had: batch is
// omitted when empty, and the whole ledger whatever --limit says, since a
// script reads the contract, not the page.
func TestLedgerBatchIsEmptyForAnUndeliveredChangeAndJSONIsTheWholeLedger(t *testing.T) {
	w := newFakeWorld()
	w.watch.ledger = threeChanges()
	w.watch.ledger.Changes[2].Batch = 0
	_, out, _ := w.run(t, "ledger")
	if !strings.Contains(out, `"#2",comment,ignored,20`) || !strings.Contains(out, `"#3",comment,pending,""`+"\n") {
		t.Errorf("batch column:\n%s", out)
	}
	code, out, _ := w.run(t, "ledger", "--limit", "1", "--json")
	if got, _ := object(t, out)["changes"].([]any); code != exitOK || len(got) != 3 || strings.Contains(out, `"batch":""`) {
		t.Errorf("--json: exit %d\n%s", code, out)
	}
}
