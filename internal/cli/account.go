package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	ucli "github.com/urfave/cli/v3"
	"golang.org/x/term"

	"github.com/gperezmz/wakewire/internal/account/application"
)

func init() {
	// The codes the account commands add to cli.md's table.
	exitFor[application.CodeNoToken] = exitNotSetUp
	exitFor[application.CodeAccountExists] = exitRefused
	exitFor[application.CodeAccountClash] = exitRefused
	exitFor[application.CodeNoSuchAccount] = exitRefused
}

// AccountsAPI is what the account commands call; *application.Service is it.
type AccountsAPI interface {
	Add(ctx context.Context, r application.AddRequest) (application.AddResult, error)
	List() ([]application.AccountView, error)
	Rename(oldName, newName string, dryRun bool) (application.RenameResult, error)
	Remove(name string, dryRun bool) (application.RemoveResult, error)
}

var dryRunFlag = &ucli.BoolFlag{Name: "dry-run", Usage: "show what would change and change nothing"}

func accountCommand(accounts AccountsAPI, s streams) *ucli.Command {
	return &ucli.Command{
		Name:  "account",
		Usage: "add, list, rename and remove the accounts wakewire reads as",
		Description: `An account pairs a host, the owners it covers and a token source with an
optional identity. A repository is read through the account whose host and
owners match; a Jira site through the account whose host it is. Accounts live
in config.toml; stored tokens live apart, in credentials.toml.

Examples:
  wakewire account list
  wakewire account add work --host github.com --owner acme --token gh:me
  wakewire account rename work job
  wakewire account remove job

Exit codes: 0 done or already so, 1 invalid value, 2 refused, 3 not set up, 4 a service failed.
Related: wakewire doctor, wakewire paths.`,
		Flags: []ucli.Flag{jsonFlag},
		Action: func(_ context.Context, cmd *ucli.Command) error {
			if cmd.Args().Present() {
				return usageError(fmt.Sprintf("unknown account command %q", cmd.Args().First()),
					"wakewire account --help lists add, list, rename and remove")
			}
			return ucli.ShowSubcommandHelp(cmd)
		},
		Commands: []*ucli.Command{
			accountAdd(accounts, s),
			accountList(accounts, s),
			accountRename(accounts, s),
			accountRemove(accounts, s),
		},
	}
}

func positional(cmd *ucli.Command, usage string, n int) ([]string, error) {
	got := cmd.Args().Slice()
	if len(got) != n {
		return nil, usageError(fmt.Sprintf("%s takes %d argument(s), got %d", cmd.FullName(), n, len(got)),
			"usage: wakewire "+usage)
	}
	return got, nil
}

func accountAdd(accounts AccountsAPI, s streams) *ucli.Command {
	return &ucli.Command{
		Name:      "add",
		Usage:     "add an account after checking its token",
		ArgsUsage: "<name>",
		Description: `Adds an account to config.toml. It checks the token first: whose it is, that
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
owner: a login, or on Jira an account ID.

Examples:
  wakewire account add work --host github.com --owner acme --token gh:me
  wakewire account add client --host gitlab.client.com --owner platform --token env:CLIENT_GITLAB_TOKEN
  pass show jira | wakewire account add jira --host acme.atlassian.net --email me@acme.com
  wakewire account add work --host github.com --owner acme --token gh:me --dry-run

Exit codes: 0 added or already so, 1 invalid value, 2 the name or the host and
owner is taken, 3 no token from the source, 4 the token was refused or the
service did not answer.
Related: wakewire account list, wakewire account remove, wakewire doctor.`,
		Flags: []ucli.Flag{
			&ucli.StringFlag{Name: "host", Usage: "github.com, a GitLab host or a Jira Cloud site"},
			&ucli.StringSliceFlag{Name: "owner", Usage: "an organization, user or group the account reads; repeatable"},
			&ucli.StringFlag{Name: "email", Usage: "the email a Jira API token belongs to"},
			&ucli.StringFlag{Name: "user", Usage: "the identity when it is not the token's owner: a login, or a Jira account ID"},
			&ucli.StringFlag{Name: "token", Value: "stored", Usage: "stored | env:<VAR> | gh:<login>"},
			dryRunFlag, jsonFlag,
		},
		Action: func(ctx context.Context, cmd *ucli.Command) error {
			a, err := positional(cmd, "account add <name> --host <host> [--owner <owner>]...", 1)
			if err != nil {
				return err
			}
			if cmd.String("host") == "" {
				return usageError("--host is required", "wakewire account add --help shows the flags")
			}
			req := application.AddRequest{
				Name: a[0], Host: cmd.String("host"), Owners: cmd.StringSlice("owner"),
				Email: cmd.String("email"), User: cmd.String("user"),
				TokenSource: cmd.String("token"), DryRun: cmd.Bool("dry-run"),
			}
			if req.TokenSource == "stored" {
				if req.Token, err = readToken(s, req.Name); err != nil {
					return err
				}
			}
			_, _ = fmt.Fprintf(s.err, "checking the token against %s\n", req.Host)
			res, err := accounts.Add(ctx, req)
			if err != nil {
				return err
			}
			return answer(s, cmd, res, addText(res))
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

func addText(r application.AddResult) string {
	a := r.Account
	what := a.Host
	if len(a.Owners) > 0 {
		what += " for " + strings.Join(a.Owners, ", ")
	}
	var head string
	switch r.Outcome {
	case application.OutcomeAdded:
		head = verb(r.DryRun, "added", "would add") + " account " + a.Name
	case application.OutcomeStored:
		head = verb(r.DryRun, "stored", "would store") + " the token of account " + a.Name
	case application.OutcomeReplaced:
		head = verb(r.DryRun, "replaced", "would replace") + " the token of account " + a.Name
	default:
		head = "account " + a.Name + " is already as asked: nothing changed"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s (%s, token %s)\n", head, what, a.TokenSource)
	if r.Identity != nil {
		fmt.Fprintf(&b, "  reads as   %s\n", identityText(*r.Identity))
		fmt.Fprintf(&b, "  can write  %s\n", writeText(r.CanWrite))
	}
	return b.String()
}

func verb(dry bool, did, would string) string {
	if dry {
		return would
	}
	return did
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
		return "unknown: the service does not say"
	case *w:
		return "yes"
	}
	return "no"
}

func accountList(accounts AccountsAPI, s streams) *ucli.Command {
	return &ucli.Command{
		Name:  "list",
		Usage: "list the accounts, without their tokens",
		Description: `Lists every account in config.toml: its name, host, service, owners, identity
override and where its token comes from. It never prints a token and never
calls a service.

Examples:
  wakewire account list
  wakewire account list --json

Exit codes: 0, 3 when config.toml does not parse.
Related: wakewire account add, wakewire doctor.`,
		Flags: []ucli.Flag{jsonFlag},
		Action: func(_ context.Context, cmd *ucli.Command) error {
			if _, err := positional(cmd, "account list", 0); err != nil {
				return err
			}
			views, err := accounts.List()
			if err != nil {
				return err
			}
			return answer(s, cmd, map[string]any{"accounts": views}, listText(views))
		},
	}
}

func listText(views []application.AccountView) string {
	if len(views) == 0 {
		return "no accounts; `wakewire account add --help` shows how to add one\n"
	}
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "NAME\tHOST\tSERVICE\tOWNERS\tTOKEN\tIDENTITY")
	for _, v := range views {
		who := v.User
		if v.Email != "" {
			who = strings.TrimSpace(v.Email + " " + who)
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", v.Name, v.Host, v.Service, strings.Join(v.Owners, ","), v.TokenSource, who)
	}
	_ = w.Flush()
	return b.String()
}

func accountRename(accounts AccountsAPI, s streams) *ucli.Command {
	return &ucli.Command{
		Name:      "rename",
		Usage:     "rename an account in config.toml and credentials.toml together",
		ArgsUsage: "<old> <new>",
		Description: `Changes an account's name in config.toml and moves its stored token to the
new name in credentials.toml. Comments and layout in config.toml stay. Run it
again and nothing changes.

Examples:
  wakewire account rename work job
  wakewire account rename work job --dry-run

Exit codes: 0 renamed or already so, 1 the new name is not a name, 2 no such
account or the new name is taken.
Related: wakewire account list, wakewire account remove.`,
		Flags: []ucli.Flag{dryRunFlag, jsonFlag},
		Action: func(_ context.Context, cmd *ucli.Command) error {
			a, err := positional(cmd, "account rename <old> <new>", 2)
			if err != nil {
				return err
			}
			res, err := accounts.Rename(a[0], a[1], cmd.Bool("dry-run"))
			if err != nil {
				return err
			}
			head := verb(res.DryRun, "renamed", "would rename") + fmt.Sprintf(" account %s to %s", res.From, res.To)
			if res.Outcome == application.OutcomeAlready {
				head = fmt.Sprintf("account %s is already named %s: nothing changed", res.From, res.To)
			}
			return answer(s, cmd, res, head+"\n")
		},
	}
}

func accountRemove(accounts AccountsAPI, s streams) *ucli.Command {
	return &ucli.Command{
		Name:      "remove",
		Usage:     "remove an account and its stored token",
		ArgsUsage: "<name>",
		Description: `Deletes the account from config.toml and its token from credentials.toml. A
stored token that no account names, which wakewire doctor warns about, is
deleted by the same command. Removing what is already gone changes nothing.

Examples:
  wakewire account remove job
  wakewire account remove job --dry-run

Exit codes: 0 removed or already gone, 3 config.toml does not parse.
Related: wakewire account list, wakewire doctor.`,
		Flags: []ucli.Flag{dryRunFlag, jsonFlag},
		Action: func(_ context.Context, cmd *ucli.Command) error {
			a, err := positional(cmd, "account remove <name>", 1)
			if err != nil {
				return err
			}
			res, err := accounts.Remove(a[0], cmd.Bool("dry-run"))
			if err != nil {
				return err
			}
			var head string
			switch {
			case res.Outcome == application.OutcomeAbsent:
				head = fmt.Sprintf("no account or stored token named %s: nothing changed", res.Name)
			case res.AccountRemoved:
				head = verb(res.DryRun, "removed", "would remove") + " account " + res.Name
				if res.TokenRemoved {
					head += " and its stored token"
				}
			default:
				head = verb(res.DryRun, "removed", "would remove") + " the stored token " + res.Name + " (no account names it)"
			}
			return answer(s, cmd, res, head+"\n")
		},
	}
}
