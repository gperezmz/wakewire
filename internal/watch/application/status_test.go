package application_test

import (
	"testing"

	"github.com/gperezmz/wakewire/internal/watch/application"
	"github.com/gperezmz/wakewire/internal/watch/application/fakes"
	"github.com/gperezmz/wakewire/internal/watch/domain"
)

// status probes the accounts over the network: it must not do so inside a
// transaction, which holds state.db's write lock against every other
// session's next and ack; see state.md, "Leases".
func TestStatusProbesAccountsOutsideTheStoreTransaction(t *testing.T) {
	h := newHarness(t)
	h.mustRegister("s1", 100)
	repo, _ := domain.ParseRepo("github.com/o/x")
	var probed, inTx int
	svc := application.New(h.store, &fakes.Sources{Source: h.source}, &fakes.Accounts{
		Forge: application.AccountIdentity{Account: "gh", Service: "GitHub", Identity: "jdoe"},
		OnDescribe: func(string) {
			probed++
			if h.store.InTx() {
				inTx++
			}
		},
	}, &fakes.Repos{Repo: repo}, h.procs, h.clock, application.Settings{Template: domain.DefaultTemplate()})
	st, err := svc.Status(h.ctx, nil)
	if err != nil || len(st) != 1 {
		t.Fatalf("%+v %v", st, err)
	}
	if probed == 0 || inTx != 0 {
		t.Fatalf("%d probe(s), %d inside a transaction", probed, inTx)
	}
	if got := st[0].Sources[0].Info.TokenSource; got != "env:TEST" {
		t.Fatalf("the probe's answer was not reported: %q", got)
	}
}
