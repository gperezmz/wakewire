// Package cli is the command-line transport: it parses arguments, calls the
// use cases the composition root built and prints their answers by the rules
// in docs/reference/cli.md.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"

	ucli "github.com/urfave/cli/v3"

	"github.com/gperezmz/wakewire/internal/wiring"
)

// Exit codes, as docs/reference/cli.md lists them.
const (
	exitOK       = 0
	exitUsage    = 1
	exitRefused  = 2
	exitNotSetUp = 3
	exitService  = 4
)

// exitFor maps an error code to its exit code.
var exitFor = map[string]int{
	"invalid_value":     exitUsage,
	"no_session":        exitNotSetUp,
	"no_source":         exitNotSetUp,
	"source_incomplete": exitNotSetUp,
	"no_account":        exitNotSetUp,
	"no_stored_token":   exitNotSetUp,
	"config_invalid":    exitNotSetUp,
	"no_token":          exitNotSetUp,
	"account_exists":    exitRefused,
	"account_conflict":  exitRefused,
	"no_such_account":   exitRefused,
	"held":              exitRefused,
	"not_registered":    exitRefused,
	"token_refused":     exitService,
	"unreachable":       exitService,
	"rate_limited":      exitService,
}

// coded is what every refusal from a use case carries: an error code from
// the CLI's table and a hint naming the fix.
type coded interface {
	error
	Code() string
	Hint() string
}

// Failure is a refusal the transport raises itself, such as a bad flag.
type Failure struct {
	code, message, hint string
}

func (f *Failure) Error() string { return f.message }
func (f *Failure) Code() string  { return f.code }
func (f *Failure) Hint() string  { return f.hint }

func usageError(message, hint string) *Failure {
	return &Failure{code: "invalid_value", message: message, hint: hint}
}

// io holds the streams one run writes to.
type streams struct {
	in       io.Reader
	out, err io.Writer
}

// Run executes one command line against the real world and returns the
// process's exit code.
func Run(ctx context.Context, args []string, build wiring.Build, stdin io.Reader, stdout, stderr io.Writer) int {
	return RunWith(ctx, args, DefaultDeps(build), stdin, stdout, stderr)
}

// RunWith executes one command line with the given dependencies.
func RunWith(ctx context.Context, args []string, deps Deps, stdin io.Reader, stdout, stderr io.Writer) int {
	s := streams{in: stdin, out: stdout, err: stderr}
	asJSON := slices.Contains(args[1:], "--json")
	root := rootCommand(deps, s)
	err := root.Run(ctx, args)
	if err == nil {
		return exitOK
	}
	return report(s, asJSON, err)
}

func report(s streams, asJSON bool, err error) int {
	if only, ok := errors.AsType[*exitOnly](err); ok {
		return only.code
	}
	var c coded
	if !errors.As(err, &c) {
		c = usageError(err.Error(), "wakewire --help lists the commands and their flags")
	}
	code, ok := exitFor[c.Code()]
	if !ok {
		code = exitUsage
	}
	if asJSON {
		writeJSON(s.out, map[string]any{"error": map[string]string{
			"code": c.Code(), "message": c.Error(), "hint": c.Hint(),
		}})
	} else {
		_, _ = fmt.Fprintf(s.err, "wakewire: %s\n%s\n", c.Error(), c.Hint())
	}
	return code
}

func writeJSON(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// answer prints a command's result: the value as one JSON object under
// --json, else the text for a person.
func answer(s streams, cmd *ucli.Command, v any, text string) error {
	if cmd.Bool("json") {
		writeJSON(s.out, v)
		return nil
	}
	_, err := io.WriteString(s.out, text)
	return err
}

var jsonFlag = &ucli.BoolFlag{Name: "json", Usage: "print exactly one JSON object on stdout"}

func rootCommand(deps Deps, s streams) *ucli.Command {
	root := &ucli.Command{
		Name:      "wakewire",
		Usage:     "wake an agent session when what it waits on changes on GitHub, GitLab or Jira",
		Writer:    s.out,
		ErrWriter: s.err,
		Reader:    s.in,
		// Errors are printed by report, in the shape cli.md fixes.
		ExitErrHandler: func(context.Context, *ucli.Command, error) {},
		HideVersion:    true,
		Flags:          []ucli.Flag{jsonFlag},
		Action: func(ctx context.Context, cmd *ucli.Command) error {
			if cmd.Args().Present() {
				return usageError(fmt.Sprintf("unknown command %q", cmd.Args().First()),
					"wakewire --help lists the commands")
			}
			return ucli.ShowAppHelp(cmd)
		},
		Commands: append(watchCommands(deps, s),
			lazyAccountCommand(deps, s),
			doctorCommand(deps, s),
			pathsCommand(deps, s),
			versionCommand(deps.Build, s),
			adapterCommand(deps, s),
		),
	}
	quietUsage(root)
	return root
}

// quietUsage makes every command report a usage error through report rather
// than print its help to stdout, which under --json must hold one object.
func quietUsage(cmd *ucli.Command) {
	cmd.OnUsageError = func(_ context.Context, c *ucli.Command, err error, _ bool) error {
		return usageError(err.Error(), c.FullName()+" --help shows its flags")
	}
	for _, sub := range cmd.Commands {
		quietUsage(sub)
	}
}

func versionCommand(build wiring.Build, s streams) *ucli.Command {
	return &ucli.Command{
		Name:  "version",
		Usage: "print the version, the commit and the wake-contract protocol",
		Description: `Prints the release tag, the commit it was built from and the number of the
wake contract this binary speaks. An adapter refuses a binary whose protocol
differs from its own.

Examples:
  wakewire version
  wakewire version --json

Exit codes: 0.
Related: wakewire doctor.`,
		Flags: []ucli.Flag{jsonFlag},
		Action: func(_ context.Context, cmd *ucli.Command) error {
			text := fmt.Sprintf("wakewire %s (commit %s, protocol %d)\n", build.Version, build.Commit, build.Protocol)
			return answer(s, cmd, build, text)
		},
	}
}
