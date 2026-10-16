package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	ucli "github.com/urfave/cli/v3"

	accountapp "github.com/gperezmz/wakewire/internal/account/application"
	"github.com/gperezmz/wakewire/internal/watch/domain"
)

// Check is one thing doctor looked at.
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Skip   bool   `json:"skipped,omitempty"`
	Detail string `json:"detail"`
	Code   string `json:"code,omitempty"`
	Fix    string `json:"fix,omitempty"`
}

type checks struct{ list []Check }

func (c *checks) ok(name, detail string) {
	c.list = append(c.list, Check{Name: name, OK: true, Detail: detail})
}
func (c *checks) skip(name, detail string) {
	c.list = append(c.list, Check{Name: name, OK: true, Skip: true, Detail: detail})
}

func (c *checks) fail(name string, err error) {
	ch := Check{Name: name, Detail: err.Error()}
	if cd, ok := errors.AsType[coded](err); ok {
		ch.Code, ch.Fix = cd.Code(), cd.Hint()
	}
	c.list = append(c.list, ch)
}

func (c *checks) warn(name, detail, fix string) {
	c.list = append(c.list, Check{Name: name, Detail: detail, Fix: fix, Code: "warning"})
}

func doctorCommand(deps Deps, s streams) *ucli.Command {
	return &ucli.Command{
		Name:  "doctor",
		Usage: "check the configuration, the accounts, this repository and the mod, and name each fix",
		Description: `Checks, in order, and names the fix for each failure: the configuration
parses; every account's token resolves, belongs to its identity and can read
what it covers; this repository's source section parses; the binary and this
session's mod speak the same protocol; no stored token is unused; no rate-limit
budget is nearly spent. The first command to run when anything looks wrong.

Under --json it prints {"ok": …, "checks": [{name, ok, detail, code, fix}]}.

Examples:
  wakewire doctor
  wakewire doctor --json

Exit codes: 0 every check passed or only warned; 3 something is not set up;
4 a service failed.
Related: wakewire status, wakewire paths, wakewire account list.`,
		Flags: []ucli.Flag{jsonFlag},
		Action: func(ctx context.Context, cmd *ucli.Command) error {
			c := &checks{}
			exit := runDoctor(ctx, deps, c)
			ok := exit == 0
			var b strings.Builder
			for _, ch := range c.list {
				mark := "ok  "
				switch {
				case ch.Skip:
					mark = "--  "
				case ch.Code == "warning":
					mark = "warn"
				case !ch.OK:
					mark = "FAIL"
				}
				fmt.Fprintf(&b, "%s %s: %s\n", mark, ch.Name, ch.Detail)
				if ch.Fix != "" && !ch.OK {
					fmt.Fprintf(&b, "     fix: %s\n", ch.Fix)
				}
			}
			if err := answer(s, cmd, map[string]any{"ok": ok, "checks": c.list}, b.String()); err != nil {
				return err
			}
			if !ok {
				return &exitOnly{code: exit}
			}
			return nil
		},
	}
}

// exitOnly ends a command with an exit code after it printed its answer.
type exitOnly struct{ code int }

func (e *exitOnly) Error() string { return fmt.Sprintf("exit %d", e.code) }

func runDoctor(ctx context.Context, deps Deps, c *checks) int {
	exit := 0
	failed := func(err error) {
		code := exitNotSetUp
		var cd coded
		if errors.As(err, &cd) && exitFor[cd.Code()] == exitService {
			code = exitService
		}
		if exit == 0 {
			exit = code
		}
	}
	acc, err := deps.Accounts()
	var views []accountapp.AccountView
	if err == nil {
		views, err = acc.List()
	}
	if err != nil {
		c.fail("configuration", err)
		failed(err)
		return exit
	}
	c.ok("configuration", fmt.Sprintf("%d account(s)", len(views)))
	for _, v := range views {
		name := "account " + v.Name
		var cred accountapp.Credential
		if v.Service == accountapp.Jira {
			cred, err = acc.ForSite(v.Host)
		} else if len(v.Owners) > 0 {
			cred, err = acc.ForRepo(v.Host, v.Owners[0])
		} else {
			err = fmt.Errorf("the account covers no owner")
		}
		if err != nil {
			c.fail(name, err)
			failed(err)
			continue
		}
		p, err := acc.Check(ctx, cred, v.Owners)
		if err != nil {
			c.fail(name, err)
			failed(err)
			continue
		}
		detail := fmt.Sprintf("%s on %s as %s (%s)", v.Service, v.Host, p.Identity.Login, v.TokenSource)
		if len(v.Owners) > 0 {
			detail += ", reads " + strings.Join(v.Owners, ", ")
		}
		if p.CanWriteKnown {
			detail += fmt.Sprintf(", can write: %v", p.CanWrite)
		}
		c.ok(name, detail)
	}

	w, err := deps.Watch(ctx)
	if err != nil {
		c.fail("state", err)
		failed(err)
		return exit
	}
	defer func() { _ = w.Close() }()
	if found, err := w.SourceCheck(ctx, deps.dir()); err != nil {
		var cd coded
		if errors.As(err, &cd) && cd.Code() == "no_source" && strings.Contains(err.Error(), "not in a git repository") {
			c.skip("source", "not in a git repository")
		} else {
			c.fail("source", err)
			failed(err)
		}
	} else {
		var vals []string
		for _, v := range found.Values {
			vals = append(vals, v.Name+"="+v.Value)
		}
		c.ok("source", fmt.Sprintf("%s, section %q (line %d): %s", found.File, found.Heading, found.HeadingLine, strings.Join(vals, " ")))
	}

	if sess := deps.Getenv("CLAUDE_CODE_SESSION_ID"); sess == "" {
		c.skip("mod", "not run from a Claude Code session")
	} else if a, err := w.AdapterOf(ctx, sess); err != nil {
		c.fail("mod", err)
		failed(err)
	} else if a == nil {
		c.warn("mod", "no wakewire mod has said hello for this session", "install it: claude plugin install wakewire --marketplace gperezmz/wakewire")
	} else if a.Protocol != domain.Protocol {
		c.warn("mod", fmt.Sprintf("the mod speaks protocol %d, this binary %d", a.Protocol, domain.Protocol),
			"install the binary and the mod from the same release")
	} else {
		c.ok("mod", fmt.Sprintf("protocol %d, heard %s", a.Protocol, a.HeardAt.Local().Format("15:04:05")))
	}

	if unused, err := acc.UnusedStored(); err != nil {
		c.fail("stored tokens", err)
		failed(err)
	} else if len(unused) > 0 {
		c.warn("stored tokens", "credentials.toml holds tokens no account names: "+strings.Join(unused, ", "),
			"wakewire account remove "+unused[0])
	} else {
		c.ok("stored tokens", "every stored token belongs to an account")
	}

	if all, err := w.Status(ctx, nil); err == nil {
		clear := true
		for _, st := range all {
			for _, p := range st.Problems {
				clear = false
				c.list = append(c.list, Check{Name: "watch " + st.Watch, Detail: p.Text, Code: p.Code, Fix: p.Fix})
				if exit == 0 {
					exit = exitFor[p.Code]
				}
			}
		}
		if clear {
			c.ok("watches", fmt.Sprintf("%d watch(es), no problem open", len(all)))
		}
		low := false
		for _, st := range all {
			for _, src := range st.Sources {
				if r := src.Rate; r != nil && domain.NearlySpent(r.Remaining, r.Limit) {
					low = true
					c.warn("budget", fmt.Sprintf("%s for %s has %d of %d requests left until %s", src.Service, st.Watch,
						r.Remaining, r.Limit, r.ResetAt.Local().Format("15:04")), "lengthen the interval: wakewire interval 5m")
				}
			}
		}
		if !low {
			c.ok("budget", "no rate limit is nearly spent")
		}
	}
	return exit
}
