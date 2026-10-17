package application_test

import (
	"fmt"
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
