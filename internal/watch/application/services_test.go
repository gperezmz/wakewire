package application_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/gperezmz/wakewire/internal/watch/application"
	"github.com/gperezmz/wakewire/internal/watch/application/fakes"
	"github.com/gperezmz/wakewire/internal/watch/domain"
)

const servicesTable = "## Forge and tracker\n\n### Services\n\n| Role | Service | Scope | Tool |\n| --- | --- | --- | --- |\n| tracker | %s | `%s` | `gh` |\n| forge | GitHub | `%s` | `gh` |\n\n### Labels\n\n| Meaning | Label |\n| --- | --- |\n| spec | `spec` |\n| ready | `ready` |\n| in progress | `in-progress` |\n| needs input | `needs-input` |\n"

func servicesSvc(h *harness, agentsMD string) *application.Service {
	repo, _ := domain.ParseRepo("github.com/o/x")
	return application.New(h.store, &fakes.Sources{Source: h.source}, &fakes.Accounts{
		Forge: application.AccountIdentity{Account: "gh", Service: "GitHub", Identity: "jdoe"},
		Jira:  application.AccountIdentity{Account: "jira", Service: "Jira", Identity: "acc-1"},
	}, &fakes.Repos{Repo: repo, Files: map[string]string{"AGENTS.md": agentsMD}}, h.procs, h.clock,
		application.Settings{Template: domain.DefaultTemplate()})
}

func TestRegisterReadsTheTrackerFromTheServicesTable(t *testing.T) {
	h := newHarness(t)
	for _, c := range []struct {
		name, md, flag, want string
	}{
		{"the forge's issues", fmt.Sprintf(servicesTable, "GitHub", "o/x", "o/x"), "", "github-issues"},
		{"Jira", fmt.Sprintf(servicesTable, "Jira", "acme.atlassian.net/KAN", "o/x"), "", "jira:acme.atlassian.net/KAN"},
		{"over a tracker line", "Tracker: gitlab-issues\n\n" + fmt.Sprintf(servicesTable, "GitHub", "o/x", "o/x"), "", "github-issues"},
		{"under the flag", fmt.Sprintf(servicesTable, "GitHub", "o/x", "o/x"), "jira:acme.atlassian.net/KAN", "jira:acme.atlassian.net/KAN"},
	} {
		r, err := servicesSvc(h, c.md).Register(h.ctx, application.RegisterInput{Session: "s", Process: 100, Tracker: c.flag, DryRun: true})
		if err != nil || r.Tracker != c.want {
			t.Errorf("%s: %q %v, want %s", c.name, r.Tracker, err, c.want)
			continue
		}
		if v := r.Source.Values[0]; v.Name != "tracker" || v.Value != c.want {
			t.Errorf("%s: first value %+v", c.name, v)
		}
	}
}

func TestRegisterRefusesAServicesTableForAnotherRepository(t *testing.T) {
	h := newHarness(t)
	for name, md := range map[string]string{
		"forge":   fmt.Sprintf(servicesTable, "GitHub", "o/x", "o/other"),
		"tracker": fmt.Sprintf(servicesTable, "GitHub", "o/other", "o/x"),
		"linear":  fmt.Sprintf(servicesTable, "Linear", "ENG", "o/x"),
	} {
		_, err := servicesSvc(h, md).Register(h.ctx, application.RegisterInput{Session: "s", Process: 100, DryRun: true})
		if code(err) != "source_incomplete" {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestDoctorReadsTheTrackerAsRegisterDoes(t *testing.T) {
	h := newHarness(t)
	found, err := servicesSvc(h, fmt.Sprintf(servicesTable, "GitHub", "o/x", "o/x")).SourceCheck(h.ctx, "")
	if err != nil || len(found.Values) != 5 || found.Values[0] != (application.ReadLine{Name: "tracker", Value: "github-issues", Line: 7}) {
		t.Fatalf("%+v %v", found, err)
	}
}

// A tracker that does not parse is the file's fault when the file gave it,
// and the caller's when --tracker did: the first names the file to correct
// (source_incomplete), the second is an invalid value.
func TestATrackerThatDoesNotParseBlamesWhereItCameFrom(t *testing.T) {
	h := newHarness(t)
	fromLine := strings.Replace(agents, "Tracker: github-issues", "Tracker: bogus-tracker", 1)
	_, err := servicesSvc(h, fromLine).Register(h.ctx, application.RegisterInput{Session: "s", Process: 100, DryRun: true})
	if code(err) != "source_incomplete" || !strings.Contains(err.Error(), "AGENTS.md, line") || !strings.Contains(err.Error(), "bogus-tracker") {
		t.Errorf("a tracker line: %v", err)
	}
	fromRow := fmt.Sprintf(servicesTable, "Jira", "not-a-site-and-key", "o/x")
	if _, err = servicesSvc(h, fromRow).Register(h.ctx, application.RegisterInput{Session: "s", Process: 100, DryRun: true}); code(err) != "source_incomplete" {
		t.Errorf("a tracker row: %v", err)
	}
	_, err = servicesSvc(h, agents).Register(h.ctx, application.RegisterInput{Session: "s", Process: 100, Tracker: "bogus-tracker", DryRun: true})
	if code(err) != "invalid_value" {
		t.Errorf("--tracker: %v", err)
	}
}

// The refusals of a source that is missing say what to type, as commands: the
// no_source one a whole register line to fill in, the tracker one the flag.
func TestMissingSourceRefusalsNameACommandToRun(t *testing.T) {
	h := newHarness(t)
	repo, _ := domain.ParseRepo("github.com/o/x")
	hintOf := func(files string) (string, string) {
		svc := application.New(h.store, &fakes.Sources{Source: h.source}, &fakes.Accounts{Forge: application.AccountIdentity{Service: "GitHub", Identity: "g"}},
			&fakes.Repos{Repo: repo, Files: map[string]string{"AGENTS.md": files}}, h.procs, h.clock, application.Settings{Template: domain.DefaultTemplate()})
		_, err := svc.Register(h.ctx, application.RegisterInput{Session: "s", Process: 100})
		var c interface {
			Code() string
			Hint() string
		}
		if !errors.As(err, &c) {
			t.Fatalf("%q: %v", files, err)
		}
		return c.Code(), c.Hint()
	}
	code, hint := hintOf("")
	if code != "no_source" || !strings.Contains(hint, "AGENTS.md") ||
		!strings.Contains(hint, "`wakewire register --tracker <tracker> --label spec=<label> --label ready=<label> --label in-progress=<label> --label needs-input=<label>`") {
		t.Errorf("no_source: %q %q", code, hint)
	}
	code, hint = hintOf("## Forge and tracker\n\n| Meaning | Label |\n|---|---|\n| spec | spec |\n| ready | ready |\n| in progress | in-progress |\n| needs input | needs-input |\n")
	if code != "source_incomplete" || !strings.Contains(hint, "`wakewire register --tracker <tracker>`") || !strings.Contains(hint, "AGENTS.md") {
		t.Errorf("no tracker: %q %q", code, hint)
	}
}
