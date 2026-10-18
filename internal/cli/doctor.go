package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/toon-format/toon-go"

	accountapp "github.com/gperezmz/wakewire/v2/internal/account/application"
	"github.com/gperezmz/wakewire/v2/internal/watch/domain"
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

// passed says whether no check failed; a warning or a skip passes.
func (c *checks) passed() bool {
	for _, ch := range c.list {
		if !ch.OK && ch.Code != "warning" {
			return false
		}
	}
	return true
}

func (c *checks) warn(name, detail, fix string) {
	c.list = append(c.list, Check{Name: name, Detail: detail, Fix: fix, Code: "warning"})
}

func doctorCommand(deps Deps, s streams) *command {
	return &command{
		name:  "doctor",
		usage: "check the configuration, the accounts, this repository and the mod, and name each fix",
		desc: `Checks, in order, and names the fix for each failure: the configuration
parses; every account's token resolves, belongs to its identity and can read
what it covers; this repository's source section parses; the binary and this
session's mod speak the same protocol; no stored token is unused; no rate-limit
budget is nearly spent. The first command to run when anything looks wrong.

It prints ok, a checks table whose result is ok, warn, fail or skipped, and a
fixes table for every check that did not pass. Under --json it prints
{"ok": …, "checks": [{name, ok, skipped, detail, code, fix}]}.`,
		examples: []string{
			"wakewire doctor",
			"wakewire doctor --json",
		},
		exits:   "0 every check passed or only warned; 1 a check failed; 2 usage.",
		related: "wakewire status, wakewire paths, wakewire account list.",
		action: func(ctx context.Context, cmd *call) error {
			c := &checks{}
			runDoctor(ctx, deps, c)
			ok := c.passed()
			if err := answer(s, cmd, map[string]any{"ok": ok, "checks": c.list}, doctorReply(c.list, ok)); err != nil {
				return err
			}
			if !ok {
				return &exitOnly{code: exitError}
			}
			return nil
		},
	}
}

func doctorReply(list []Check, ok bool) *reply {
	rows := make([]toon.Object, len(list))
	var fixes []toon.Object
	for i, ch := range list {
		result := "ok"
		switch {
		case ch.Skip:
			result = "skipped"
		case ch.Code == "warning":
			result = "warn"
		case !ch.OK:
			result = "fail"
		}
		rows[i] = obj("name", ch.Name, "result", result, "detail", ch.Detail)
		if !ch.OK && ch.Fix != "" {
			code := ch.Code
			if code == "" {
				code = "none"
			}
			fixes = append(fixes, obj("name", ch.Name, "code", code, "fix", ch.Fix))
		}
	}
	out := newReply().add("ok", ok).add("checks", rows)
	if len(fixes) > 0 {
		out.add("fixes", fixes)
	}
	return out
}

// exitOnly ends a command with an exit code after it printed its answer.
type exitOnly struct{ code int }

func (e *exitOnly) Error() string { return fmt.Sprintf("exit %d", e.code) }

func runDoctor(ctx context.Context, deps Deps, c *checks) {
	acc, err := deps.Accounts()
	var views []accountapp.AccountView
	if err == nil {
		views, err = acc.List()
	}
	if err != nil {
		c.fail("configuration", err)
		return
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
			continue
		}
		p, err := acc.Check(ctx, cred, v.Owners)
		if err != nil {
			c.fail(name, err)
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

	if unused, err := acc.UnusedStored(); err != nil {
		c.fail("stored tokens", err)
	} else if len(unused) > 0 {
		c.warn("stored tokens", "credentials.toml holds tokens no account names: "+strings.Join(unused, ", "),
			"Run `wakewire account remove "+unused[0]+"`")
	} else {
		c.ok("stored tokens", "every stored token belongs to an account")
	}

	// A check changes nothing, so state.db is opened dry.
	w, err := deps.Watch(ctx, true)
	if err != nil {
		c.fail("state", err)
		for _, name := range []string{"source", "mod", "watches", "budget"} {
			c.skip(name, "not checked: state.db did not open")
		}
		return
	}
	defer func() { _ = w.Close() }()
	if found, err := w.SourceCheck(ctx, deps.dir()); err != nil {
		var cd coded
		if errors.As(err, &cd) && cd.Code() == "no_source" && strings.Contains(err.Error(), "not in a git repository") {
			c.skip("source", "not in a git repository")
		} else {
			c.fail("source", err)
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
	} else if a == nil {
		c.warn("mod", "no wakewire mod has said hello for this session", "install it: claude plugin install wakewire --marketplace gperezmz/wakewire")
	} else if a.Protocol != domain.Protocol {
		c.warn("mod", fmt.Sprintf("the mod speaks protocol %d, this binary %d", a.Protocol, domain.Protocol),
			"install the binary and the mod from the same release")
	} else {
		c.ok("mod", fmt.Sprintf("protocol %d, heard %s", a.Protocol, ago(time.Now(), a.HeardAt)))
	}

	all, err := w.Status(ctx, nil)
	if err != nil {
		c.fail("watches", err)
		c.skip("budget", "not checked: the watches did not load")
		return
	}
	{
		clear := true
		for _, st := range all {
			for _, p := range st.Problems {
				clear = false
				c.list = append(c.list, Check{Name: "watch " + st.Watch, Detail: p.Text, Code: p.Code, Fix: p.Fix})
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
					c.warn("budget", fmt.Sprintf("%s for %s has %d of %d requests left; it resets %s", src.Service, st.Watch,
						r.Remaining, r.Limit, ahead(time.Now(), r.ResetAt)), "lengthen the interval: wakewire interval <duration>")
				}
			}
		}
		if !low {
			c.ok("budget", "no rate limit is nearly spent")
		}
	}
}
