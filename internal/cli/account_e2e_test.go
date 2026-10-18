package cli

import (
	"bytes"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gperezmz/wakewire/internal/adapters/home"
	"github.com/gperezmz/wakewire/internal/wiring"
)

// Made-up tokens: none is real.
const (
	goodToken    = "ghp_GOODTOKEN0123456789"   //nolint:gosec // a made-up token
	refusedToken = "ghp_REFUSEDTOKEN987654321" //nolint:gosec // a made-up token
	ghToken      = "ghp_FROMGHCOMMAND5555"     //nolint:gosec // a made-up token
)

// world is a real account service over a temp home, a fake GitHub and a fake gh.
type world struct {
	t     *testing.T
	paths home.Paths
	deps  Deps
	hits  *[]string
}

func newWorld(t *testing.T) *world {
	t.Helper()
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits = append(hits, r.Method+" "+r.URL.Path)
		auth := r.Header.Get("Authorization")
		if auth != "Bearer "+goodToken && auth != "Bearer "+ghToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("X-OAuth-Scopes", "repo, read:org")
		switch r.URL.Path {
		case "/user":
			_, _ = w.Write([]byte(`{"login":"octo","id":583231,"name":"The Octocat"}`))
		case "/users/ACME", "/users/octo":
			_, _ = w.Write([]byte(`{"login":"ACME","id":1}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	gh := filepath.Join(t.TempDir(), "gh")
	script := "#!/bin/sh\necho " + ghToken + "\n"
	if err := os.WriteFile(gh, []byte(script), 0o700); err != nil { //nolint:gosec // a test script must be executable
		t.Fatal(err)
	}

	dir := filepath.Join(t.TempDir(), "wakewire-home")
	paths, err := home.Resolve(func(k string) string {
		if k == "WAKEWIRE_HOME" {
			return dir
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	api, err := wiring.NewAccountsWith(paths, func(string) string { return "" }, wiring.AccountOptions{
		GitHubAPI: srv.URL,
		Origin:    func(string) string { return srv.URL },
		GHBinary:  gh,
	})
	if err != nil {
		t.Fatal(err)
	}
	deps := newFakeWorld().deps()
	deps.Accounts = func() (DoctorAccounts, error) { return api, nil }
	return &world{t: t, paths: paths, deps: deps, hits: &hits}
}

// run runs a command and fails the test when the token shows up in anything
// it printed.
func (w *world) run(stdin string, args ...string) (int, string, string) {
	w.t.Helper()
	code, out, errOut := runDeps(w.deps, stdin, append([]string{"account"}, args...)...)
	for _, tok := range []string{goodToken, refusedToken, ghToken} {
		if strings.Contains(out, tok) || strings.Contains(errOut, tok) {
			w.t.Fatalf("a token was printed by %v:\nstdout: %s\nstderr: %s", args, out, errOut)
		}
	}
	return code, out, errOut
}

// leaks lists every file under the home folder that holds a token, other
// than credentials.toml.
func (w *world) leaks(tokens ...string) []string {
	w.t.Helper()
	var found []string
	_ = filepath.WalkDir(w.paths.Home, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path == w.paths.Credentials {
			return nil
		}
		data, _ := os.ReadFile(path) //nolint:gosec // a temp folder the test owns
		for _, tok := range tokens {
			if bytes.Contains(data, []byte(tok)) {
				found = append(found, path)
			}
		}
		return nil
	})
	return found
}

func TestAccountLifecycleNeverPrintsOrLeaksAToken(t *testing.T) {
	w := newWorld(t)

	// add, with the token on stdin
	code, out, _ := w.run(goodToken+"\n", "add", "work", "--host", "github.com", "--owner", "ACME", "--json")
	got := object(t, out)
	if code != 0 || got["outcome"] != "added" || got["canWrite"] != true || got["identity"].(map[string]any)["login"] != "octo" {
		t.Fatalf("exit %d: %v", code, got)
	}
	creds, err := os.ReadFile(w.paths.Credentials)
	if err != nil || !strings.Contains(string(creds), goodToken) {
		t.Fatalf("the stored token is not in credentials.toml: %v", err)
	}
	if info, _ := os.Stat(w.paths.Credentials); info.Mode().Perm() != 0o600 {
		t.Fatalf("credentials.toml mode %v", info.Mode())
	}
	cfg, _ := os.ReadFile(w.paths.Config)
	if !strings.Contains(string(cfg), `name   = "work"`) || !strings.Contains(string(cfg), `token  = "stored"`) {
		t.Fatalf("config.toml:\n%s", cfg)
	}

	// the same again changes nothing and asks no service
	before := len(*w.hits)
	code, out, _ = w.run(goodToken+"\n", "add", "work", "--host", "github.com", "--owner", "ACME", "--json")
	if got := object(t, out); code != 0 || got["outcome"] != "already" || len(*w.hits) != before {
		t.Fatalf("repeat: exit %d %v, %d new requests", code, got, len(*w.hits)-before)
	}

	// list
	code, out, _ = w.run("", "list", "--json")
	if code != 0 || strings.Contains(out, "token\":\""+goodToken) {
		t.Fatalf("list: exit %d %s", code, out)
	}
	if accounts := object(t, out)["accounts"].([]any); len(accounts) != 1 {
		t.Fatalf("list: %s", out)
	}

	// a gh source resolves through the gh command
	code, out, errOut := w.run("", "add", "viagh", "--host", "github.com", "--owner", "octo", "--token", "gh:octo")
	if code != 0 || !strings.Contains(out, "account: viagh\noutcome: added") || !strings.Contains(out, `token: "gh:octo"`) {
		t.Fatalf("gh add: exit %d %s %s", code, out, errOut)
	}
	if !strings.Contains(errOut, "checking the token against github.com") {
		t.Fatalf("progress belongs on stderr: %q", errOut)
	}

	// rename and remove, dry runs first
	if code, _, _ = w.run("", "rename", "work", "job", "--dry-run"); code != 0 {
		t.Fatal("dry-run rename failed")
	}
	if cfg, _ := os.ReadFile(w.paths.Config); !strings.Contains(string(cfg), `"work"`) {
		t.Fatal("a dry run changed config.toml")
	}
	if code, _, errOut = w.run("", "rename", "work", "job"); code != 0 {
		t.Fatalf("rename: %s", errOut)
	}
	if creds, _ := os.ReadFile(w.paths.Credentials); !strings.Contains(string(creds), "[job]") || strings.Contains(string(creds), "[work]") {
		t.Fatalf("credentials.toml after rename:\n%s", creds)
	}
	if code, out, _ = w.run("", "remove", "job"); code != 0 || !strings.Contains(out, "account: job\noutcome: removed\ntokenRemoved: true") {
		t.Fatalf("remove: %d %s", code, out)
	}
	if code, out, _ = w.run("", "remove", "job"); code != 0 || !strings.Contains(out, "account: job\noutcome: none by that name (no-op)") {
		t.Fatalf("second remove: %d %s", code, out)
	}
	if creds, _ := os.ReadFile(w.paths.Credentials); strings.Contains(string(creds), goodToken) {
		t.Fatal("the removed account's token is still stored")
	}

	if files := w.leaks(goodToken, refusedToken, ghToken); len(files) != 0 {
		t.Fatalf("a token was written outside credentials.toml: %v", files)
	}
	for _, h := range *w.hits {
		if !strings.HasPrefix(h, "GET ") {
			t.Fatalf("a request wrote: %s", h)
		}
	}
}

func TestARefusedTokenIsReportedWithoutTheTokenAndWritesNothing(t *testing.T) {
	w := newWorld(t)
	code, out, errOut := w.run(refusedToken+"\n", "add", "work", "--host", "github.com", "--owner", "ACME", "--json")
	got := object(t, out)
	e, _ := got["error"].(map[string]any)
	if code != exitError || e["code"] != "token_refused" || e["hint"] == "" {
		t.Fatalf("exit %d: %s %s", code, out, errOut)
	}
	for _, p := range []string{w.paths.Config, w.paths.Credentials} {
		if _, err := os.Stat(p); err == nil {
			t.Fatalf("%s was written for a refused token", p)
		}
	}
	// and without --json, as TOON on stdout; stderr holds only the progress
	code, out, errOut = w.run(refusedToken+"\n", "add", "work", "--host", "github.com", "--owner", "ACME")
	if code != exitError || !strings.Contains(out, "code: token_refused") || !strings.Contains(out, "error: GitHub refused the token") ||
		errOut != "checking the token against github.com\n" {
		t.Fatalf("exit %d stdout %q stderr %q", code, out, errOut)
	}
	if files := w.leaks(refusedToken); len(files) != 0 {
		t.Fatalf("the refused token was written: %v", files)
	}
}

func TestAnOwnerTheTokenCannotReadIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	w := newWorld(t)
	code, out, _ := w.run(goodToken+"\n", "add", "work", "--host", "github.com", "--owner", "ghost", "--json")
	e, _ := object(t, out)["error"].(map[string]any)
	if code != exitUsage || e["code"] != "invalid_value" {
		t.Fatalf("exit %d: %s", code, out)
	}
	if _, err := os.Stat(w.paths.Config); err == nil {
		t.Fatal("config.toml was written")
	}
}

func TestAddingOverAnotherAccountIsRefused(t *testing.T) {
	w := newWorld(t)
	if code, _, errOut := w.run("", "add", "one", "--host", "github.com", "--owner", "ACME", "--token", "gh:octo"); code != 0 {
		t.Fatal(errOut)
	}
	code, out, _ := w.run("", "add", "two", "--host", "github.com", "--owner", "acme", "--token", "gh:octo", "--json")
	e, _ := object(t, out)["error"].(map[string]any)
	if code != exitError || e["code"] != "account_conflict" {
		t.Fatalf("exit %d: %s", code, out)
	}
}

// A credentials.toml entry with an empty token is listed by doctor as unused;
// `account remove` takes it out and says a stored token was removed.
func TestRemoveTakesOutAStoredEntryWithAnEmptyToken(t *testing.T) {
	w := newWorld(t)
	if err := os.MkdirAll(w.paths.Home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.paths.Credentials, []byte("[ghost]\ntoken = \"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, _ := w.run("", "remove", "ghost", "--json")
	got := object(t, out)
	if code != 0 || got["outcome"] != "removed" || got["tokenRemoved"] != true {
		t.Fatalf("exit %d: %s", code, out)
	}
	if data, _ := os.ReadFile(w.paths.Credentials); strings.Contains(string(data), "ghost") {
		t.Fatalf("the entry is still stored:\n%s", data)
	}
	code, out, _ = w.run("", "remove", "ghost")
	if code != 0 || !strings.Contains(out, "outcome: none by that name (no-op)") {
		t.Fatalf("repeat: exit %d %s", code, out)
	}
}
