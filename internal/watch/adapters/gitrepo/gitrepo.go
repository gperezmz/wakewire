// Package gitrepo reads the repository a command runs in, through git.
package gitrepo

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gperezmz/wakewire/internal/watch/domain"
)

// Repos runs git, and ssh to resolve a host alias.
type Repos struct {
	Git, SSH string // the commands; "git" and "ssh" when empty
}

func (r Repos) run(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func or(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

// Root answers the top of the working tree holding dir.
func (r Repos) Root(ctx context.Context, dir string) (string, error) {
	out, err := r.run(ctx, dir, or(r.Git, "git"), "rev-parse", "--show-toplevel")
	if err != nil {
		return "", domain.Refuse("no_source", fmt.Sprintf("%s is not in a git repository", dir),
			"run it from the repository, or pass --repo, --tracker and every --label")
	}
	return out, nil
}

// Origin answers the repository the origin remote points at, as
// <host>/<path>, with an ssh host alias resolved to its real host.
func (r Repos) Origin(ctx context.Context, dir string) (domain.Repo, error) {
	raw, err := r.run(ctx, dir, or(r.Git, "git"), "remote", "get-url", "origin")
	if err != nil || raw == "" {
		return domain.Repo{}, domain.Invalid(fmt.Sprintf("%s has no origin remote", dir),
			"pass --repo <host>/<owner>/<repo>, or run it from a clone with an origin")
	}
	host, path, ssh, err := ParseRemote(raw)
	if err != nil {
		return domain.Repo{}, err
	}
	if ssh {
		if real := r.sshHost(ctx, host); real != "" {
			host = real
		}
	}
	return domain.ParseRepo(host + "/" + path)
}

// sshHost asks ssh which host an alias in ~/.ssh/config names.
func (r Repos) sshHost(ctx context.Context, alias string) string {
	out, err := r.run(ctx, "", or(r.SSH, "ssh"), "-G", alias)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		if h, ok := strings.CutPrefix(line, "hostname "); ok {
			return strings.TrimSpace(h)
		}
	}
	return ""
}

// ParseRemote reads a git remote URL: scp-like `git@host:o/r.git`,
// `ssh://git@host[:port]/o/r.git`, or `https://host/o/r.git`. ssh says
// whether host may be an ssh alias.
func ParseRemote(raw string) (host, path string, ssh bool, err error) {
	bad := domain.Invalid(fmt.Sprintf("the origin remote %q is not a forge URL", raw), "pass --repo <host>/<owner>/<repo>")
	if strings.Contains(raw, "://") {
		u, perr := url.Parse(raw)
		if perr != nil || u.Hostname() == "" {
			return "", "", false, bad
		}
		host, path, ssh = u.Hostname(), u.Path, u.Scheme == "ssh" || u.Scheme == "git+ssh"
	} else {
		at := strings.LastIndex(raw, "@")
		h, p, ok := strings.Cut(raw[at+1:], ":")
		if !ok {
			return "", "", false, bad
		}
		host, path, ssh = h, p, true
	}
	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	if host == "" || !strings.Contains(path, "/") {
		return "", "", false, bad
	}
	return strings.ToLower(host), path, ssh, nil
}

// ReadFile answers a file of the repository, and false when it is absent.
func (Repos) ReadFile(root, name string) (string, bool, error) {
	b, err := os.ReadFile(filepath.Join(root, filepath.Clean("/"+name)))
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	return string(b), err == nil, err
}
