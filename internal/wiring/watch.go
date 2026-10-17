package wiring

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"time"

	"github.com/gperezmz/wakewire/internal/adapters/config"
	"github.com/gperezmz/wakewire/internal/adapters/home"
	"github.com/gperezmz/wakewire/internal/adapters/httpread"
	"github.com/gperezmz/wakewire/internal/watch/adapters/accounts"
	"github.com/gperezmz/wakewire/internal/watch/adapters/gitrepo"
	"github.com/gperezmz/wakewire/internal/watch/adapters/proc"
	"github.com/gperezmz/wakewire/internal/watch/adapters/sources"
	"github.com/gperezmz/wakewire/internal/watch/adapters/sqlitestore"
	"github.com/gperezmz/wakewire/internal/watch/application"
	"github.com/gperezmz/wakewire/internal/watch/domain"
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
			return nil, err
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
		return nil, err
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

// openState opens state.db. A dry run over a home with no state.db opens an
// empty one in memory instead of creating the file.
func openState(ctx context.Context, paths home.Paths, dryRun bool) (*sqlitestore.Store, error) {
	if dryRun {
		if _, err := os.Stat(paths.State); errors.Is(err, fs.ErrNotExist) {
			return sqlitestore.OpenMemory(ctx)
		}
	}
	return sqlitestore.Open(ctx, paths.State)
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
