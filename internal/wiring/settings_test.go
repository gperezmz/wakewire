package wiring

import (
	"reflect"
	"testing"
	"time"

	"github.com/gperezmz/wakewire/v2/internal/adapters/config"
	"github.com/gperezmz/wakewire/v2/internal/watch/domain"
)

// Each label meaning is read from its own row of the source template: a
// swapped pair would pass the defaults, where no two keys are alike only by
// accident, so every key here is distinct.
func TestSettingsMapEachLabelMeaningToItsTemplateKey(t *testing.T) {
	cfg := config.Defaults()
	cfg.Source.Files = []string{"TEAM.md"}
	cfg.Source.Heading = "Where things live"
	cfg.Source.Tracker = "Board:"
	cfg.Source.Table.Key = "Role"
	cfg.Source.Table.Value = "Name"
	cfg.Source.Labels.Spec = "key-for-spec"
	cfg.Source.Labels.Ready = "key-for-ready"
	cfg.Source.Labels.InProgress = "key-for-in-progress"
	cfg.Source.Labels.NeedsInput = "key-for-needs-input"
	cfg.Echo.Marker = "my marker"
	cfg.Poll.Interval = "90s"
	got, err := Settings(cfg)
	if err != nil {
		t.Fatal(err)
	}
	want := map[domain.Meaning]string{
		domain.MeaningSpec: "key-for-spec", domain.MeaningReady: "key-for-ready",
		domain.MeaningInProgress: "key-for-in-progress", domain.MeaningNeedsInput: "key-for-needs-input",
	}
	if !reflect.DeepEqual(got.Template.Keys, want) {
		t.Fatalf("keys %v, want %v", got.Template.Keys, want)
	}
	tpl := got.Template
	if !reflect.DeepEqual(tpl.Files, []string{"TEAM.md"}) || tpl.Heading != "Where things live" || tpl.Tracker != "Board:" ||
		tpl.TableKey != "Role" || tpl.TableValue != "Name" {
		t.Fatalf("%+v", tpl)
	}
	if got.Marker != "my marker" || got.Interval != 90*time.Second {
		t.Fatalf("%q %v", got.Marker, got.Interval)
	}
}

// config.toml's defaults and the domain's default template are two spellings
// of one thing; they must not drift apart.
func TestTheConfigurationsDefaultsAreTheDomainsDefaultTemplate(t *testing.T) {
	got, err := Settings(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Template, domain.DefaultTemplate()) {
		t.Fatalf("%+v\n%+v", got.Template, domain.DefaultTemplate())
	}
	if got.Interval != domain.DefaultInterval {
		t.Fatalf("%v", got.Interval)
	}
}

func TestSettingsRefuseAnIntervalTooShort(t *testing.T) {
	cfg := config.Defaults()
	cfg.Poll.Interval = "10s"
	if _, err := Settings(cfg); err == nil {
		t.Fatal("an interval under 30s was accepted")
	}
}
