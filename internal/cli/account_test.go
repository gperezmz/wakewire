package cli

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/gperezmz/wakewire/v2/internal/account/application"
)

func yes() *bool { b := true; return &b }

func TestAddPassesFlagsAndReadsTheStoredTokenFromStdin(t *testing.T) {
	w := newFakeWorld()
	w.stdin = "the-token\n"
	w.acc.addRes = application.AddResult{
		Outcome: "added", Account: application.AccountView{Name: "work", Host: "github.com", Owners: []string{"A", "B"}, TokenSource: "stored"},
		Identity: &application.Identity{Login: "me", Name: "Me Too"}, CanWrite: yes(),
	}
	code, out, errOut := w.run(t, "account", "add", "work", "--host", "github.com", "--owner", "A", "--owner", "B", "--user", "me", "--dry-run")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	got := w.acc.added[0]
	if got.Name != "work" || got.Host != "github.com" || !slices.Equal(got.Owners, []string{"A", "B"}) || got.User != "me" ||
		got.Token != "the-token" || got.TokenSource != "stored" || !got.DryRun {
		t.Fatalf("request %+v", got)
	}
	for _, want := range []string{"account: work", "outcome: added", "host: github.com", "owners[2]: A,B", "token: stored",
		"readsAs: me (Me Too)", "canWrite: yes"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "the-token") || strings.Contains(errOut, "the-token") {
		t.Fatal("the token was printed")
	}
}

func TestAddWithAnEnvOrGHSourceReadsNoStdin(t *testing.T) {
	w := newFakeWorld()
	w.stdin = "must-not-be-read"
	code, _, errOut := w.run(t, "account", "add", "w", "--host", "github.com", "--owner", "A", "--token", "gh:me")
	if code != 0 || w.acc.added[0].Token != "" || w.acc.added[0].TokenSource != "gh:me" {
		t.Fatalf("exit %d %+v %s", code, w.acc.added, errOut)
	}
}

func TestAddJSONIsOneObject(t *testing.T) {
	w := newFakeWorld()
	w.stdin = "t"
	w.acc.addRes = application.AddResult{Outcome: "already", Account: application.AccountView{Name: "w", Owners: []string{}}}
	code, out, _ := w.run(t, "account", "add", "w", "--host", "h", "--owner", "o", "--json")
	if got := object(t, out); code != 0 || got["outcome"] != "already" || got["identity"] != nil {
		t.Fatalf("exit %d %q", code, out)
	}
}

// account add names what it did, or under --dry-run what it would do, in the
// same words as the other commands.
func TestAccountAddSaysWhatItDidOrWouldDo(t *testing.T) {
	for _, c := range []struct {
		result  string
		dry     bool
		outcome string
	}{
		{application.OutcomeAdded, false, "outcome: added\n"},
		{application.OutcomeAdded, true, "outcome: would add\n"},
		{application.OutcomeStored, false, "outcome: stored the token\n"},
		{application.OutcomeStored, true, "outcome: would store the token\n"},
		{application.OutcomeReplaced, false, "outcome: replaced the token\n"},
		{application.OutcomeReplaced, true, "outcome: would replace the token\n"},
		{application.OutcomeAlready, false, "outcome: already as asked (no-op)\n"},
		{application.OutcomeAlready, true, "outcome: already as asked (no-op)\n"},
	} {
		w := newFakeWorld()
		w.acc.addRes = application.AddResult{Outcome: c.result, DryRun: c.dry, Account: application.AccountView{Name: "w", Host: "h", TokenSource: "gh:me"}}
		code, out, _ := w.run(t, "account", "add", "w", "--host", "h", "--owner", "o", "--token", "gh:me")
		if code != exitOK || !strings.Contains(out, c.outcome) {
			t.Errorf("%s dry=%v: exit %d\n%s", c.result, c.dry, code, out)
		}
	}
}

func TestAddWithoutATokenIsAUsageError(t *testing.T) {
	w := newFakeWorld()
	w.stdin = "  \n"
	code, out, _ := w.run(t, "account", "add", "w", "--host", "github.com", "--owner", "A", "--json")
	if code != exitUsage || errorCode(t, out) != "invalid_value" {
		t.Fatalf("exit %d %q", code, out)
	}
}

// Every refusal exits 1, whatever the service or the setup said; only a
// command line wakewire cannot run as written exits 2.
func TestErrorCodesMapToExitCodes(t *testing.T) {
	for code, want := range map[string]int{
		"no_account": exitError, "no_stored_token": exitError, "no_token": exitError, "config_invalid": exitError,
		"account_exists": exitError, "account_conflict": exitError, "no_such_account": exitError,
		"token_refused": exitError, "unreachable": exitError, "rate_limited": exitError, "internal": exitError,
		"invalid_value": exitUsage, "usage": exitUsage,
	} {
		w := newFakeWorld()
		w.acc.listErr = application.NewError(code, "msg "+code, "hint "+code)
		got, out, _ := w.run(t, "account", "list", "--json")
		if got != want || !strings.Contains(out, `"hint":"hint `+code+`"`) {
			t.Errorf("%s: exit %d want %d, stdout %q", code, got, want, out)
		}
	}
}

func TestListTOONAndJSON(t *testing.T) {
	w := newFakeWorld()
	w.acc.views = []application.AccountView{
		{Name: "work", Host: "github.com", Service: "github", Owners: []string{"A", "B"}, TokenSource: "gh:me", User: "me"},
		{Name: "jira", Host: "s.atlassian.net", Service: "jira", Owners: []string{}, Email: "a@b.c", TokenSource: "stored"},
	}
	_, out, _ := w.run(t, "account", "list")
	for _, want := range []string{"accounts[2]{name,host,service,token}:", `work,github.com,github,"gh:me"`, "jira,s.atlassian.net,jira,stored"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	for _, not := range []string{"A B", "a@b.c"} {
		if strings.Contains(out, not) {
			t.Errorf("the default columns hold %q:\n%s", not, out)
		}
	}
	// --fields adds columns; each holds the owners, the email and the user.
	_, out, _ = w.run(t, "account", "list", "--fields", "owners,email,user")
	for _, want := range []string{"accounts[2]{name,host,service,token,owners,email,user}:", `work,github.com,github,"gh:me",A B,"",me`,
		`jira,s.atlassian.net,jira,stored,"",a@b.c,""`} {
		if !strings.Contains(out, want) {
			t.Errorf("--fields lacks %q:\n%s", want, out)
		}
	}
	_, out, _ = w.run(t, "account", "list", "--json")
	var got struct{ Accounts []map[string]any }
	if err := json.Unmarshal([]byte(out), &got); err != nil || len(got.Accounts) != 2 || got.Accounts[0]["token"] != "gh:me" {
		t.Fatalf("%q %v", out, err)
	}
	_, out, _ = newFakeWorld().run(t, "account", "list")
	if !strings.Contains(out, "accounts: 0 accounts in config.toml") {
		t.Fatalf("%q", out)
	}
}

func TestRenameAndRemovePassTheirArgumentsAndDryRun(t *testing.T) {
	w := newFakeWorld()
	if code, out, _ := w.run(t, "account", "rename", "a", "b", "--dry-run"); code != 0 || !strings.Contains(out, "from: a\nto: b\noutcome: would rename") {
		t.Fatalf("%d %q", code, out)
	}
	if code, out, _ := w.run(t, "account", "remove", "a"); code != 0 || !strings.Contains(out, "account: a\noutcome: removed") {
		t.Fatalf("%d %q", code, out)
	}
	if w.acc.renamed[0] != [3]any{"a", "b", true} || w.acc.removed[0] != [2]any{"a", false} {
		t.Fatalf("%v %v", w.acc.renamed, w.acc.removed)
	}
}

// `wakewire account` alone lists the accounts, as `account list` does, and
// under --json prints the same object.
func TestAccountAloneListsTheAccounts(t *testing.T) {
	w := newFakeWorld()
	w.acc.views = []application.AccountView{{Name: "work", Host: "github.com", Service: "github", Owners: []string{"A"}, TokenSource: "stored"}}
	code, out, errOut := w.run(t, "account")
	_, want, _ := w.run(t, "account", "list")
	if code != exitOK || errOut != "" || out != want || !strings.Contains(out, "accounts[1]{name,host,service,token}:") {
		t.Errorf("exit %d stderr %q\n%s\nwant\n%s", code, errOut, out, want)
	}
	_, out, _ = w.run(t, "account", "--json")
	_, want, _ = w.run(t, "account", "list", "--json")
	if out != want || len(object(t, out)["accounts"].([]any)) != 1 {
		t.Errorf("--json:\n%s\nwant\n%s", out, want)
	}
}

// account add checks its flags before it reads the token, so a person who
// forgot --owner is told so rather than asked for a token first; the hint
// shows both forms of the command.
func TestAddChecksItsFlagsBeforeReadingTheToken(t *testing.T) {
	for name, args := range map[string][]string{
		"no owner":      {"account", "add", "w", "--host", "github.com"},
		"jira no email": {"account", "add", "j", "--host", "s.atlassian.net"},
	} {
		// Empty stdin: were the token read first, the refusal would be "no token was given".
		w := newFakeWorld()
		code, out, errOut := w.run(t, append(args, "--json")...)
		e, _ := object(t, out)["error"].(map[string]any)
		if code != exitUsage || e["code"] != "invalid_value" || strings.Contains(e["message"].(string), "no token") || len(w.acc.added) != 0 {
			t.Errorf("%s: exit %d\n%s", name, code, out)
		}
		for _, want := range []string{"`wakewire account add <name> --host <host> --owner <owner>`",
			"`wakewire account add <name> --host <site>.atlassian.net --email <email>`"} {
			if !strings.Contains(e["hint"].(string), want) {
				t.Errorf("%s: the hint lacks %q: %v", name, want, e["hint"])
			}
		}
		if strings.Contains(errOut, "checking the token") || strings.Contains(errOut, "hidden") {
			t.Errorf("%s: the token was asked for before the flags were checked: %q", name, errOut)
		}
	}
}
