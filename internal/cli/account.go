package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/toon-format/toon-go"
	"golang.org/x/term"

	"github.com/gperezmz/wakewire/internal/account/application"
)

// AccountsAPI is what the account commands call; *application.Service is it.
type AccountsAPI interface {
	Add(ctx context.Context, r application.AddRequest) (application.AddResult, error)
	List() ([]application.AccountView, error)
	Rename(oldName, newName string, dryRun bool) (application.RenameResult, error)
	Remove(name string, dryRun bool) (application.RemoveResult, error)
}

var dryRunFlag = flagSpec{name: "dry-run", kind: boolFlag, usage: "show what would change and change nothing"}

func accountCommand(accounts AccountsAPI, s streams) *command {
	return &command{
		name:  "account",
		usage: "add, list, rename and remove the accounts wakewire reads as",
		desc: `An account pairs a host, the owners it covers and a token source with an
optional identity. A repository is read through the account whose host and
owners match; a Jira site through the account whose host it is. Accounts live
in config.toml; stored tokens live apart, in credentials.toml. Run alone, it
lists the accounts, as account list does.`,
		examples: []string{
			"wakewire account list",
			"wakewire account add work --host github.com --owner acme --token gh:me",
			"wakewire account rename work job",
			"wakewire account remove job",
		},
		exits:   "0 done or already so; 1 refused, not set up or a service failed; 2 usage.",
		related: "wakewire doctor, wakewire paths.",
		action: func(_ context.Context, cmd *call) error {
			views, err := accounts.List()
			if err != nil {
				return err
			}
			return answer(s, cmd, map[string]any{"accounts": views}, listReply(views, nil))
		},
		subs: []*command{
			accountAdd(accounts, s),
			accountList(accounts, s),
			accountRename(accounts, s),
			accountRemove(accounts, s),
		},
	}
}

func accountAdd(accounts AccountsAPI, s streams) *command {
	return &command{
		name:  "add",
		usage: "add an account after checking its token",
		args:  []arg{{name: "name"}},
		desc: `Adds an account to config.toml. It checks the token first: whose it is, that
it can read every --owner and whether it can write; only then does it write
config.toml and, for --token stored, credentials.toml.

--token says where the token comes from: stored (the default) reads it from a
hidden prompt when stdin is a terminal and from stdin otherwise, never from a
flag; env:<VAR> reads the environment variable <VAR> when needed; gh:<login>
asks the gh command for that login's token when needed.

Run it again with the same flags and nothing changes. Run it again for an
existing account with --token stored and another token and the stored token is
replaced; that is how to replace a refused one. A Jira Cloud site (a host
ending .atlassian.net) takes --email and no --owner; any other host takes
--owner at least once. --user sets the identity when it is not the token's
owner: a login, or on Jira an account ID.`,
		examples: []string{
			"wakewire account add work --host github.com --owner acme --token gh:me",
			"wakewire account add client --host gitlab.client.com --owner platform --token env:CLIENT_GITLAB_TOKEN",
			"pass show jira | wakewire account add jira --host acme.atlassian.net --email me@acme.com",
			"wakewire account add work --host github.com --owner acme --token gh:me --dry-run",
		},
		exits: `0 added or already so; 1 the name or the host and owner is taken,
no token came from the source, or the service refused it or did not answer;
2 usage or an invalid value.`,
		related: "wakewire account list, wakewire account remove, wakewire doctor.",
		flags: []flagSpec{
			{name: "host", kind: stringFlag, value: "<host>", usage: "github.com, a GitLab host or a Jira Cloud site (required)"},
			{name: "owner", kind: listFlag, value: "<owner>", usage: "an organization, user or group the account reads; repeatable"},
			{name: "email", kind: stringFlag, value: "<email>", usage: "the email a Jira API token belongs to"},
			{name: "user", kind: stringFlag, value: "<identity>", usage: "the identity when it is not the token's owner: a login, or a Jira account ID"},
			{name: "token", kind: stringFlag, value: "<source>", def: "stored", usage: "stored | env:<VAR> | gh:<login>"},
			dryRunFlag,
		},
		action: func(ctx context.Context, cmd *call) error {
			a := cmd.args
			if cmd.String("host") == "" {
				return badUsage("--host is required", "wakewire account add <name> --host <host> [--owner <owner>]... [--token stored|env:<VAR>|gh:<login>]")
			}
			req := application.AddRequest{
				Name: a[0], Host: cmd.String("host"), Owners: cmd.Strings("owner"),
				Email: cmd.String("email"), User: cmd.String("user"),
				TokenSource: cmd.String("token"), DryRun: cmd.Bool("dry-run"),
			}
			// Every flag is checked before the token is asked for, so a person
			// never types a token for a request that is refused anyway.
			if err := req.Check(); err != nil {
				return err
			}
			if req.TokenSource == "stored" {
				var err error
				if req.Token, err = readToken(s, req.Name); err != nil {
					return err
				}
			}
			_, _ = fmt.Fprintf(s.err, "checking the token against %s\n", req.Host)
			res, err := accounts.Add(ctx, req)
			if err != nil {
				return err
			}
			return answer(s, cmd, res, addReply(res))
		},
	}
}

// readToken takes the token from a hidden prompt when stdin is a terminal,
// else from the first line of stdin. It is never echoed or kept elsewhere.
func readToken(s streams, name string) (string, error) {
	var raw string
	if f, ok := s.in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		_, _ = fmt.Fprintf(s.err, "Token for %s (hidden): ", name)
		b, err := term.ReadPassword(int(f.Fd()))
		_, _ = fmt.Fprintln(s.err)
		if err != nil {
			return "", usageError("could not read the token from the terminal", "pipe it on stdin instead")
		}
		raw = string(b)
	} else {
		b, err := io.ReadAll(io.LimitReader(s.in, 1<<16))
		if err != nil {
			return "", usageError("could not read the token from stdin", "pipe it on stdin, or run the command in a terminal")
		}
		raw, _, _ = strings.Cut(strings.TrimSpace(string(b)), "\n")
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", usageError("no token was given for --token stored",
			"pipe it on stdin (`… | wakewire account add …`), or run the command in a terminal to be asked; a token is never taken from a flag")
	}
	return raw, nil
}

func addReply(r application.AddResult) *reply {
	a := r.Account
	outcome := "already as asked (no-op)"
	if w, ok := map[string][2]string{application.OutcomeAdded: {"added", "would add"},
		application.OutcomeStored:   {"stored the token", "would store the token"},
		application.OutcomeReplaced: {"replaced the token", "would replace the token"}}[r.Outcome]; ok {
		outcome = dryWord(r.DryRun, w[0], w[1])
	}
	out := newReply().add("account", a.Name).add("outcome", outcome).add("host", a.Host)
	if len(a.Owners) > 0 {
		out.add("owners", a.Owners)
	}
	out.add("token", a.TokenSource)
	if r.Identity != nil {
		out.add("readsAs", identityText(*r.Identity)).add("canWrite", writeText(r.CanWrite))
	}
	return out.hint("Run `wakewire doctor` to check every account", "Run `wakewire register` in a repository it covers to start a watch")
}

func identityText(id application.Identity) string {
	s := id.Login
	if id.Name != "" && id.Name != id.Login {
		s += " (" + id.Name + ")"
	}
	return s
}

func writeText(w *bool) string {
	switch {
	case w == nil:
		return "unknown"
	case *w:
		return "yes"
	}
	return "no"
}

func accountList(accounts AccountsAPI, s streams) *command {
	return &command{
		name:  "list",
		usage: "list the accounts, without their tokens",
		desc: `Lists every account in config.toml: its name, host, service, owners, identity
override and where its token comes from. It never prints a token and never
calls a service.`,
		examples: []string{
			"wakewire account list",
			"wakewire account list --json",
		},
		exits:   "0; 1 config.toml does not parse; 2 usage.",
		related: "wakewire account add, wakewire doctor.",
		flags:   []flagSpec{fieldsFlag(accountExtra)},
		action: func(_ context.Context, cmd *call) error {
			extra, err := pickFields(cmd, accountExtra)
			if err != nil {
				return err
			}
			views, err := accounts.List()
			if err != nil {
				return err
			}
			return answer(s, cmd, map[string]any{"accounts": views}, listReply(views, extra))
		},
	}
}

// accountExtra are the account list's columns beyond name, host, service and token.
var accountExtra = []string{"owners", "email", "user"}

func listReply(views []application.AccountView, extra []string) *reply {
	if len(views) == 0 {
		return newReply().add("accounts", "0 accounts in config.toml").
			hint("Run `wakewire account add <name> --host <host> --owner <owner> --token env:<VAR>` to add one")
	}
	rows := make([]toon.Object, len(views))
	for i, v := range views {
		rows[i] = withColumns(obj("name", v.Name, "host", v.Host, "service", string(v.Service), "token", v.TokenSource),
			extra, map[string]any{"owners": strings.Join(v.Owners, " "), "email": v.Email, "user": v.User})
	}
	return newReply().add("accounts", rows).
		hint("Run `wakewire account list --fields owners,user` for what each covers and reads as", "Run `wakewire doctor` to check each token")
}

func accountRename(accounts AccountsAPI, s streams) *command {
	return &command{
		name:  "rename",
		usage: "rename an account in config.toml and credentials.toml together",
		args:  []arg{{name: "old"}, {name: "new"}},
		desc: `Changes an account's name in config.toml and moves its stored token to the
new name in credentials.toml. Comments and layout in config.toml stay. Run it
again and nothing changes.`,
		examples: []string{
			"wakewire account rename work job",
			"wakewire account rename work job --dry-run",
		},
		exits: `0 renamed or already so; 1 no such account or the new name is
taken; 2 usage or the new name is not a name.`,
		related: "wakewire account list, wakewire account remove.",
		flags:   []flagSpec{dryRunFlag},
		action: func(_ context.Context, cmd *call) error {
			a := cmd.args
			res, err := accounts.Rename(a[0], a[1], cmd.Bool("dry-run"))
			if err != nil {
				return err
			}
			outcome := dryWord(res.DryRun, "renamed", "would rename")
			if res.Outcome == application.OutcomeAlready {
				outcome = "already so (no-op)"
			}
			return answer(s, cmd, res, newReply().add("from", res.From).add("to", res.To).add("outcome", outcome))
		},
	}
}

func accountRemove(accounts AccountsAPI, s streams) *command {
	return &command{
		name:  "remove",
		usage: "remove an account and its stored token",
		args:  []arg{{name: "name"}},
		desc: `Deletes the account from config.toml and its token from credentials.toml. A
stored token that no account names, which wakewire doctor warns about, is
deleted by the same command. Removing what is already gone changes nothing.`,
		examples: []string{
			"wakewire account remove job",
			"wakewire account remove job --dry-run",
		},
		exits:   "0 removed or already gone; 1 config.toml does not parse; 2 usage.",
		related: "wakewire account list, wakewire doctor.",
		flags:   []flagSpec{dryRunFlag},
		action: func(_ context.Context, cmd *call) error {
			a := cmd.args
			res, err := accounts.Remove(a[0], cmd.Bool("dry-run"))
			if err != nil {
				return err
			}
			out := newReply().add("account", res.Name)
			switch {
			case res.Outcome == application.OutcomeAbsent:
				out.add("outcome", "none by that name (no-op)")
			case res.AccountRemoved:
				out.add("outcome", dryWord(res.DryRun, "removed", "would remove")).add("tokenRemoved", res.TokenRemoved)
			default:
				out.add("outcome", dryWord(res.DryRun, "removed the unused stored token", "would remove the unused stored token"))
			}
			return answer(s, cmd, res, out)
		},
	}
}
