// Package ghcli asks the gh command for a login's token: `gh auth token
// --hostname <host> --user <login>`, run with an argument vector and no shell.
package ghcli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// timeout bounds one gh run.
const timeout = 10 * time.Second

// overrides are the variables with which gh takes a token from the
// environment, ahead of the logins it keeps. They are taken out of the child's
// environment so that the login the account names is the one that answers.
var overrides = []string{"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN"}

// CLI runs gh.
type CLI struct {
	Binary  string          // "gh" when empty
	Environ func() []string // os.Environ when nil
}

// Token returns the token gh holds for login on host. A failure says what gh
// said; gh prints a token only on success, and that never reaches an error.
func (c CLI) Token(host, login string) (string, error) {
	bin := c.Binary
	if bin == "" {
		bin = "gh"
	}
	environ := os.Environ
	if c.Environ != nil {
		environ = c.Environ
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "auth", "token", "--hostname", host, "--user", login) //nolint:gosec // an argument vector with no shell; host and login are config values
	cmd.Env = scrub(environ())
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		switch {
		case errors.Is(err, exec.ErrNotFound), errors.Is(err, os.ErrNotExist):
			return "", fmt.Errorf("%s is not installed or not on PATH", bin)
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			return "", fmt.Errorf("%s did not answer within %s", bin, timeout)
		}
		return "", errors.New(firstLine(stderr.String(), err.Error()))
	}
	token := strings.TrimSpace(stdout.String())
	if token == "" {
		return "", fmt.Errorf("%s printed no token", bin)
	}
	return token, nil
}

func scrub(environ []string) []string {
	out := make([]string, 0, len(environ))
next:
	for _, kv := range environ {
		for _, name := range overrides {
			if strings.HasPrefix(kv, name+"=") {
				continue next
			}
		}
		out = append(out, kv)
	}
	return out
}

func firstLine(s, fallback string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}
	line, _, _ := strings.Cut(s, "\n")
	if len(line) > 200 {
		line = line[:200]
	}
	return line
}
