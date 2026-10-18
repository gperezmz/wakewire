package cli

import (
	"context"
	"os"
	"time"

	accountapp "github.com/gperezmz/wakewire/internal/account/application"
	"github.com/gperezmz/wakewire/internal/adapters/home"
	"github.com/gperezmz/wakewire/internal/watch/application"
	"github.com/gperezmz/wakewire/internal/wiring"
)

// WatchAPI is what the watch commands call; *wiring.Watch is it.
type WatchAPI interface {
	Register(ctx context.Context, in application.RegisterInput) (application.RegisterResult, error)
	Unregister(ctx context.Context, sel application.Selector, dryRun bool) (application.UnregisterResult, error)
	Pause(ctx context.Context, sel application.Selector, d time.Duration, dryRun bool) (application.PauseResult, error)
	Resume(ctx context.Context, sel application.Selector, dryRun bool) (application.PauseResult, error)
	Status(ctx context.Context, sel *application.Selector) ([]application.WatchStatus, error)
	LocalStatus(ctx context.Context, sel *application.Selector) ([]application.WatchStatus, error)
	Ledger(ctx context.Context, sel application.Selector, since time.Duration, item string) (application.LedgerResult, error)
	Changes(ctx context.Context, batch int64) (application.BatchView, error)
	SetInterval(ctx context.Context, sel application.Selector, d time.Duration, dryRun bool) (application.IntervalResult, error)
	Prune(ctx context.Context, dryRun bool) (application.PruneCounts, error)
	Hello(ctx context.Context, session string, proc, protocol int) (application.HelloResult, error)
	Next(ctx context.Context, session string, proc int) (application.NextResult, error)
	Ack(ctx context.Context, batch int64) (application.AckResult, error)
	Ended(ctx context.Context, session, reason string) (application.EndedResult, error)
	SourceCheck(ctx context.Context, dir string) (application.SourceFound, error)
	AdapterOf(ctx context.Context, session string) (*application.Adapter, error)
	Close() error
}

// DoctorAccounts is what doctor asks of the accounts.
type DoctorAccounts interface {
	AccountsAPI
	ForRepo(host, owner string) (accountapp.Credential, error)
	ForSite(host string) (accountapp.Credential, error)
	Check(ctx context.Context, c accountapp.Credential, owners []string) (accountapp.Probe, error)
	UnusedStored() ([]string, error)
}

// Deps are what a run needs from the world, built only when a command
// asks for them, so `version` opens nothing.
type Deps struct {
	Build  wiring.Build
	Getenv func(string) string
	Getwd  func() (string, error)
	PPID   func() int
	// Executable and UserHome name the running binary in the home view.
	Executable func() (string, error)
	UserHome   func() (string, error)
	Paths      func() (home.Paths, error)
	// Watch opens the watch service; a dry run asks for one that creates nothing.
	Watch    func(ctx context.Context, dryRun bool) (WatchAPI, error)
	Accounts func() (DoctorAccounts, error)
}

// DefaultDeps are the real world's.
func DefaultDeps(build wiring.Build) Deps {
	paths := func() (home.Paths, error) {
		p, err := home.Resolve(os.Getenv)
		if err != nil {
			return p, usageError(err.Error(), "set WAKEWIRE_HOME to the folder wakewire keeps its files in")
		}
		return p, nil
	}
	return Deps{
		Build:      build,
		Getenv:     os.Getenv,
		Getwd:      os.Getwd,
		PPID:       os.Getppid,
		Executable: os.Executable,
		UserHome:   os.UserHomeDir,
		Paths:      paths,
		Watch: func(ctx context.Context, dryRun bool) (WatchAPI, error) {
			p, err := paths()
			if err != nil {
				return nil, err
			}
			return wiring.NewWatch(ctx, p, os.Getenv, wiring.WatchOptions{DryRun: dryRun})
		},
		Accounts: func() (DoctorAccounts, error) {
			p, err := paths()
			if err != nil {
				return nil, err
			}
			return wiring.NewAccounts(p, os.Getenv)
		},
	}
}
