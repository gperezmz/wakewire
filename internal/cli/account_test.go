package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	ucli "github.com/urfave/cli/v3"

	"github.com/gperezmz/wakewire/internal/account/application"
)

// runAccount runs the account command the way Run runs a command: under a
// root with --json, usage errors reported and errors printed by report.
func runAccount(api AccountsAPI, stdin string, args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	s := streams{in: strings.NewReader(stdin), out: &out, err: &errOut}
	root := &ucli.Command{
		Name: "wakewire", Writer: s.out, ErrWriter: s.err, Reader: s.in,
		ExitErrHandler: func(context.Context, *ucli.Command, error) {},
		HideVersion:    true,
		Flags:          []ucli.Flag{jsonFlag},
		Commands:       []*ucli.Command{accountCommand(api, s)},
	}
	quietUsage(root)
	full := append([]string{"wakewire", "account"}, args...)
	if err := root.Run(context.Background(), full); err != nil {
		return report(s, slices.Contains(args, "--json"), err), out.String(), errOut.String()
	}
	return 0, out.String(), errOut.String()
}

type fakeAPI struct {
	added    []application.AddRequest
	addRes   application.AddResult
	err      error
	renamed  [][3]any
	removed  [][2]any
	accounts []application.AccountView
}

func (f *fakeAPI) Add(_ context.Context, r application.AddRequest) (application.AddResult, error) {
	f.added = append(f.added, r)
	return f.addRes, f.err
}
func (f *fakeAPI) List() ([]application.AccountView, error) { return f.accounts, f.err }
func (f *fakeAPI) Rename(o, n string, dry bool) (application.RenameResult, error) {
	f.renamed = append(f.renamed, [3]any{o, n, dry})
	return application.RenameResult{Outcome: "renamed", DryRun: dry, From: o, To: n}, f.err
}
func (f *fakeAPI) Remove(n string, dry bool) (application.RemoveResult, error) {
	f.removed = append(f.removed, [2]any{n, dry})
	return application.RemoveResult{Outcome: "removed", DryRun: dry, Name: n, AccountRemoved: true}, f.err
}

func yes() *bool { b := true; return &b }

func TestAddPassesFlagsAndReadsTheStoredTokenFromStdin(t *testing.T) {
	api := &fakeAPI{addRes: application.AddResult{
		Outcome: "added", Account: application.AccountView{Name: "work", Host: "github.com", Owners: []string{"A", "B"}, TokenSource: "stored"},
		Identity: &application.Identity{Login: "me", Name: "Me Too"}, CanWrite: yes(),
	}}
	code, out, errOut := runAccount(api, "the-token\n", "add", "work", "--host", "github.com", "--owner", "A", "--owner", "B", "--user", "me", "--dry-run")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	got := api.added[0]
	if got.Name != "work" || got.Host != "github.com" || !slices.Equal(got.Owners, []string{"A", "B"}) || got.User != "me" ||
		got.Token != "the-token" || got.TokenSource != "stored" || !got.DryRun {
		t.Fatalf("request %+v", got)
	}
	for _, want := range []string{"added account work", "github.com for A, B", "reads as   me (Me Too)", "can write  yes"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "the-token") || strings.Contains(errOut, "the-token") {
		t.Fatal("the token was printed")
	}
}

func TestAddWithAnEnvOrGHSourceReadsNoStdin(t *testing.T) {
	api := &fakeAPI{}
	code, _, errOut := runAccount(api, "must-not-be-read", "add", "w", "--host", "github.com", "--owner", "A", "--token", "gh:me")
	if code != 0 || api.added[0].Token != "" || api.added[0].TokenSource != "gh:me" {
		t.Fatalf("exit %d %+v %s", code, api.added, errOut)
	}
}

func TestAddJSONIsOneObject(t *testing.T) {
	api := &fakeAPI{addRes: application.AddResult{Outcome: "already", Account: application.AccountView{Name: "w", Owners: []string{}}}}
	code, out, _ := runAccount(api, "t", "add", "w", "--host", "h", "--json")
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil || code != 0 || got["outcome"] != "already" || got["identity"] != nil {
		t.Fatalf("exit %d %q %v", code, out, err)
	}
}

func TestUsageErrorsExitOneWithAHint(t *testing.T) {
	cases := map[string][]string{
		"no host":          {"add", "w", "--json"},
		"no name":          {"add", "--host", "h", "--json"},
		"too many":         {"rename", "a", "b", "c", "--json"},
		"list with an arg": {"list", "x", "--json"},
		"unknown":          {"nope", "--json"},
		"unknown flag":     {"add", "w", "--host", "h", "--nope", "--json"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			code, out, _ := runAccount(&fakeAPI{}, "tok", args...)
			var got struct{ Error struct{ Code, Hint string } }
			if code != 1 || json.Unmarshal([]byte(out), &got) != nil || got.Error.Code != "invalid_value" || got.Error.Hint == "" {
				t.Fatalf("exit %d, stdout %q", code, out)
			}
		})
	}
}

func TestAddWithoutATokenIsAUsageError(t *testing.T) {
	code, out, _ := runAccount(&fakeAPI{}, "  \n", "add", "w", "--host", "github.com", "--owner", "A", "--json")
	if code != 1 || !strings.Contains(out, "invalid_value") {
		t.Fatalf("exit %d %q", code, out)
	}
}

type codedErr struct{ code string }

func (e codedErr) Error() string { return "msg " + e.code }
func (e codedErr) Code() string  { return e.code }
func (e codedErr) Hint() string  { return "hint " + e.code }

func TestErrorCodesMapToExitCodes(t *testing.T) {
	for code, want := range map[string]int{
		"no_account": 3, "no_stored_token": 3, "no_token": 3, "config_invalid": 3,
		"account_exists": 2, "account_conflict": 2, "no_such_account": 2,
		"token_refused": 4, "unreachable": 4, "rate_limited": 4, "invalid_value": 1,
	} {
		got, out, _ := runAccount(&fakeAPI{err: codedErr{code}}, "t", "list", "--json")
		if got != want || !strings.Contains(out, `"hint":"hint `+code+`"`) {
			t.Errorf("%s: exit %d want %d, stdout %q", code, got, want, out)
		}
	}
}

func TestListTextAndJSON(t *testing.T) {
	api := &fakeAPI{accounts: []application.AccountView{
		{Name: "work", Host: "github.com", Service: "github", Owners: []string{"A", "B"}, TokenSource: "gh:me", User: "me"},
		{Name: "jira", Host: "s.atlassian.net", Service: "jira", Owners: []string{}, Email: "a@b.c", TokenSource: "stored"},
	}}
	_, out, _ := runAccount(api, "", "list")
	for _, want := range []string{"NAME", "work", "github.com", "A,B", "gh:me", "a@b.c"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	_, out, _ = runAccount(api, "", "list", "--json")
	var got struct{ Accounts []map[string]any }
	if err := json.Unmarshal([]byte(out), &got); err != nil || len(got.Accounts) != 2 || got.Accounts[0]["token"] != "gh:me" {
		t.Fatalf("%q %v", out, err)
	}
	_, out, _ = runAccount(&fakeAPI{}, "", "list")
	if !strings.Contains(out, "no accounts") {
		t.Fatalf("%q", out)
	}
}

func TestRenameAndRemovePassTheirArgumentsAndDryRun(t *testing.T) {
	api := &fakeAPI{}
	if code, out, _ := runAccount(api, "", "rename", "a", "b", "--dry-run"); code != 0 || !strings.Contains(out, "would rename account a to b") {
		t.Fatalf("%d %q", code, out)
	}
	if code, out, _ := runAccount(api, "", "remove", "a"); code != 0 || !strings.Contains(out, "removed account a") {
		t.Fatalf("%d %q", code, out)
	}
	if api.renamed[0] != [3]any{"a", "b", true} || api.removed[0] != [2]any{"a", false} {
		t.Fatalf("%v %v", api.renamed, api.removed)
	}
}

func TestEveryAccountCommandHelpSaysWhatItDoes(t *testing.T) {
	for _, sub := range []string{"add", "list", "rename", "remove"} {
		_, out, _ := runAccount(&fakeAPI{}, "", sub, "--help")
		for _, want := range []string{"Examples:", "Exit codes:", "Related:"} {
			if !strings.Contains(out, want) {
				t.Errorf("account %s --help lacks %q:\n%s", sub, want, out)
			}
		}
	}
}
