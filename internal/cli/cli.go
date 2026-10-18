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

	"github.com/gperezmz/wakewire/v2/internal/wiring"
)

// Exit codes, as docs/reference/cli.md lists them: the error code on stdout
// says which refusal it was.
const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

// usageCodes are the error codes of a command line wakewire cannot run as
// written; every other code is a refusal of a command it understood.
var usageCodes = map[string]bool{"usage": true, "invalid_value": true}

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

// usageError refuses a flag's or an argument's value.
func usageError(message, hint string) *Failure {
	return &Failure{code: "invalid_value", message: message, hint: hint}
}

// badUsage refuses a command line's shape: an unknown command or flag, or a
// missing or extra argument.
func badUsage(message, hint string) *Failure {
	return &Failure{code: "usage", message: message, hint: hint}
}

// streams are what one run reads from and writes to.
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
	if version, ok := fastVersion(args[1:], deps.Build); ok {
		_, _ = fmt.Fprintln(stdout, version)
		return exitOK
	}
	c := parse(rootCommand(deps, s), args[1:])
	switch {
	case c.Bool("help"):
		writeHelp(stdout, c.cmd)
		return exitOK
	case c.err != nil:
		return report(s, c.Bool("json"), c.err)
	}
	if err := c.cmd.action(ctx, c); err != nil {
		return report(s, c.Bool("json"), err)
	}
	return exitOK
}

// refuse refuses what the state forbids, under its own code: exit 1.
func refuse(code, message, hint string) *Failure {
	return &Failure{code: code, message: message, hint: hint}
}

// fastVersion answers -v, -V and --version, alone, with the bare version,
// before any command is built.
func fastVersion(args []string, build wiring.Build) (string, bool) {
	if len(args) != 1 || (args[0] != "-v" && args[0] != "-V" && args[0] != "--version") {
		return "", false
	}
	return build.Version, true
}

// report prints a refusal on stdout, as TOON or under --json as JSON, and
// answers the exit code: 2 for a command line wakewire cannot run as
// written, 1 for every other refusal.
func report(s streams, asJSON bool, err error) int {
	if only, ok := errors.AsType[*exitOnly](err); ok {
		return only.code
	}
	var c coded
	if !errors.As(err, &c) {
		c = &Failure{code: "internal", message: err.Error(),
			hint: "this is a bug in wakewire: report it with the command and its output"}
	}
	code := exitError
	if usageCodes[c.Code()] {
		code = exitUsage
	}
	if asJSON {
		writeJSON(s.out, map[string]any{"error": map[string]string{
			"code": c.Code(), "message": c.Error(), "hint": c.Hint(),
		}})
	} else {
		_, _ = io.WriteString(s.out, newReply().add("error", c.Error()).add("code", c.Code()).hint(c.Hint()).toon())
	}
	return code
}

func writeJSON(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// rootCommand is the table of every command; its action, run alone, is the
// home view.
func rootCommand(deps Deps, s streams) *command {
	return (&command{
		name:  "wakewire",
		usage: description,
		desc: `Run alone, it shows this binary, the watch of the repository it runs in and the
next steps. Every command prints TOON on stdout, errors included; --json prints
one JSON object instead. Progress goes to stderr.`,
		exits: `0 done, including "already so"; 1 refused, not set up or a service
failed (the error's code says which); 2 usage: an unknown command or flag, a
missing or extra argument, or an invalid value.`,
		action: func(ctx context.Context, cmd *call) error { return homeView(ctx, deps, s, cmd) },
		subs: append(watchCommands(deps, s),
			lazyAccountCommand(deps, s),
			doctorCommand(deps, s),
			pathsCommand(deps, s),
			versionCommand(deps.Build, s),
			adapterCommand(deps, s),
		),
	}).link()
}

func versionCommand(build wiring.Build, s streams) *command {
	return &command{
		name:  "version",
		usage: "print the version, the commit and the wake-contract protocol",
		desc: `Prints the release tag, the commit it was built from and the number of the
wake contract this binary speaks. An adapter refuses a binary whose protocol
differs from its own.

-v, -V and --version print the bare version alone.`,
		examples: []string{"wakewire version", "wakewire --version", "wakewire version --json"},
		exits:    "0; 2 usage.",
		related:  "wakewire doctor.",
		action: func(_ context.Context, cmd *call) error {
			return answer(s, cmd, build, newReply().add("version", build.Version).add("commit", build.Commit).add("protocol", build.Protocol))
		},
	}
}
