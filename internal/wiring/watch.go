package wiring

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gperezmz/wakewire/v2/internal/adapters/config"
	"github.com/gperezmz/wakewire/v2/internal/adapters/home"
	"github.com/gperezmz/wakewire/v2/internal/adapters/httpread"
	"github.com/gperezmz/wakewire/v2/internal/watch/adapters/accounts"
	"github.com/gperezmz/wakewire/v2/internal/watch/adapters/gitrepo"
	"github.com/gperezmz/wakewire/v2/internal/watch/adapters/proc"
	"github.com/gperezmz/wakewire/v2/internal/watch/adapters/sources"
	"github.com/gperezmz/wakewire/v2/internal/watch/adapters/sqlitestore"
	"github.com/gperezmz/wakewire/v2/internal/watch/application"
	"github.com/gperezmz/wakewire/v2/internal/watch/domain"
)

// WatchOptions are the seams a test replaces; the zero value is the real world.
type WatchOptions struct {
	Account   AccountOptions
	Clock     application.Clock
	Processes application.Processes
	// DryRun: the command changes nothing, so it creates nothing either.
	// Where state.db does not exist yet, the service runs over an empty
	// one in memory, and no folder or file is made.
	DryRun bool
	// Self is this binary's own process, which holds the poll lease; the
	// zero value is the running process.
	Self application.Process
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

// Watch is the watch service and what it holds open.
type Watch struct {
	*application.Service
	Store    *sqlitestore.Store
	Settings application.Settings
}

// Close closes state.db.
func (w *Watch) Close() error { return w.Store.Close() }

// NewWatch builds the watch service over state.db, the configuration, the
// accounts and the services.
func NewWatch(ctx context.Context, paths home.Paths, getenv func(string) string, o WatchOptions) (*Watch, error) {
	if !o.DryRun {
		if err := paths.Ensure(); err != nil {
			return nil, domain.Refuse("home_unwritable", fmt.Sprintf("cannot make %s: %v", paths.Home, err),
				"make the folder writable, or set WAKEWIRE_HOME to one that is; `wakewire paths` shows where it is")
		}
	}
	cfg, err := config.Load(paths.Config)
	if err != nil {
		return nil, err
	}
	settings, err := Settings(cfg)
	if err != nil {
		return nil, err
	}
	acct, err := NewAccountsWith(paths, getenv, o.Account)
	if err != nil {
		return nil, err
	}
	store, err := openState(ctx, paths, o.DryRun)
	if err != nil {
		return nil, unreadableState(paths.State, err)
	}
	var clock application.Clock = systemClock{}
	if o.Clock != nil {
		clock = o.Clock
	}
	var procs application.Processes = proc.Table{}
	if o.Processes != nil {
		procs = o.Processes
	}
	// Where the table cannot name this process, the lease falls back to the
	// session's process, as the commands that need the table fail anyway.
	self := o.Self
	if self.PID == 0 {
		if start, running, err := procs.StartOf(os.Getpid()); err == nil && running {
			self = application.Process{PID: os.Getpid(), Start: start}
		}
	}
	acl := accounts.Accounts{Service: acct}
	var transport http.RoundTripper
	if o.Account.Transport != nil {
		transport = o.Account.Transport
	}
	src := sources.Sources{Accounts: acl, HTTP: httpread.New("wakewire", transport),
		Endpoints: sources.Endpoints{GitHub: o.Account.GitHubAPI, Origin: o.Account.Origin}}
	svc := application.New(store, src, acl, gitrepo.Repos{}, procs, clock, settings, application.WithSelf(self))
	return &Watch{Service: svc, Store: store, Settings: settings}, nil
}

// unreadableState names a state.db that does not open as what it is: the
// user's file to fix or move aside, not a bug in wakewire. A refusal the
// store raised itself, such as state_newer, keeps its own code.
func unreadableState(path string, err error) error {
	if _, ok := errors.AsType[*domain.Refusal](err); ok {
		return err
	}
	return domain.Refuse("state_unreadable", fmt.Sprintf("%s does not open: %s", path, strings.TrimPrefix(err.Error(), "state.db at "+path+": ")),
		"move it aside to start with an empty state, or restore it from a backup; `wakewire paths` shows where it is")
}

// openState opens state.db. A dry run writes nothing: over a home with no
// state.db it opens an empty one in memory, and over one that exists it
// opens it dry, so not even a migration is kept.
func openState(ctx context.Context, paths home.Paths, dryRun bool) (*sqlitestore.Store, error) {
	if !dryRun {
		return sqlitestore.Open(ctx, paths.State)
	}
	if _, err := os.Stat(paths.State); errors.Is(err, fs.ErrNotExist) {
		return sqlitestore.OpenMemory(ctx)
	}
	return sqlitestore.OpenDry(ctx, paths.State)
}

// Settings maps the configuration's values onto the watch context's.
func Settings(cfg config.File) (application.Settings, error) {
	interval := domain.DefaultInterval
	if cfg.Poll.Interval != "" {
		d, err := domain.ParseInterval(cfg.Poll.Interval)
		if err != nil {
			return application.Settings{}, err
		}
		interval = d
	}
	s := cfg.Source
	return application.Settings{
		Interval: interval,
		Marker:   cfg.Echo.Marker,
		Template: domain.Template{
			Files: s.Files, Heading: s.Heading, Tracker: s.Tracker, TableKey: s.Table.Key, TableValue: s.Table.Value,
			Keys: map[domain.Meaning]string{
				domain.MeaningSpec: s.Labels.Spec, domain.MeaningReady: s.Labels.Ready,
				domain.MeaningInProgress: s.Labels.InProgress, domain.MeaningNeedsInput: s.Labels.NeedsInput,
			},
			Services: domain.ServicesTable{Role: s.Services.Role, Service: s.Services.Service, Scope: s.Services.Scope,
				Tracker: s.Services.Tracker, Forge: s.Services.Forge},
		},
	}, nil
}
