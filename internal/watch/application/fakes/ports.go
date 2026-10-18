package fakes

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/gperezmz/wakewire/v2/internal/watch/application"
	"github.com/gperezmz/wakewire/v2/internal/watch/domain"
)

// Clock is a clock the test moves.
type Clock struct {
	mu sync.Mutex
	T  time.Time
}

func (c *Clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.T }

// Advance moves the clock on.
func (c *Clock) Advance(d time.Duration) { c.mu.Lock(); c.T = c.T.Add(d); c.mu.Unlock() }

// Processes is a process table the test writes.
type Processes struct {
	mu      sync.Mutex
	Running map[int]string
}

func (p *Processes) StartOf(pid int) (string, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s, ok := p.Running[pid]
	return s, ok, nil
}

// Start adds a running process.
func (p *Processes) Start(pid int, start string) { p.mu.Lock(); p.Running[pid] = start; p.mu.Unlock() }

// Kill removes a process, as kill -9 would.
func (p *Processes) Kill(pid int) { p.mu.Lock(); delete(p.Running, pid); p.mu.Unlock() }

// Repos is one repository with its files.
type Repos struct {
	Repo  domain.Repo
	Files map[string]string
}

func (r *Repos) Origin(context.Context, string) (domain.Repo, error) { return r.Repo, nil }
func (r *Repos) Root(context.Context, string) (string, error)        { return "/repo", nil }
func (r *Repos) ReadFile(_, name string) (string, bool, error) {
	s, ok := r.Files[name]
	return s, ok, nil
}

// Accounts answers one forge identity and one Jira identity.
type Accounts struct {
	Forge, Jira application.AccountIdentity
	// OnDescribe, when set, is called with each account Describe is asked about.
	OnDescribe func(account string)
}

func (a *Accounts) ForForge(context.Context, domain.Repo) (application.AccountIdentity, error) {
	return a.Forge, nil
}

func (a *Accounts) ForJira(context.Context, string) (application.AccountIdentity, error) {
	if a.Jira.Account == "" {
		return a.Jira, errors.New("no jira account")
	}
	return a.Jira, nil
}

func (a *Accounts) Describe(_ context.Context, account string) (application.AccountInfo, error) {
	if a.OnDescribe != nil {
		a.OnDescribe(account)
	}
	return application.AccountInfo{TokenSource: "env:TEST"}, nil
}

// Source answers each poll with the next scripted answer, or with what
// Items shows when the script runs out.
type Source struct {
	mu      sync.Mutex
	Name    string
	Script  []func(application.PollInput) (application.PollOutput, error)
	Polls   int
	Blocker chan struct{} // when set, a poll waits on it
}

// PollCount answers how many polls began.
func (s *Source) PollCount() int { s.mu.Lock(); defer s.mu.Unlock(); return s.Polls }

func (s *Source) Service() string { return s.Name }
func (s *Source) Account() string { return "test" }

func (s *Source) Poll(ctx context.Context, in application.PollInput) (application.PollOutput, error) {
	s.mu.Lock()
	s.Polls++
	var step func(application.PollInput) (application.PollOutput, error)
	if len(s.Script) > 0 {
		step, s.Script = s.Script[0], s.Script[1:]
	}
	b := s.Blocker
	s.mu.Unlock()
	if b != nil {
		select {
		case <-b:
		case <-ctx.Done():
			return application.PollOutput{}, ctx.Err()
		}
	}
	if step == nil {
		return application.PollOutput{}, nil
	}
	return step(in)
}

// Sources binds one source to every watch.
type Sources struct {
	Source *Source
	Rules  func(application.Watch) domain.Rules
}

func (s *Sources) For(_ context.Context, w application.Watch) ([]application.BoundSource, error) {
	r := domain.Rules{Identity: w.Identity, Labels: w.Labels, Marker: w.Marker}
	if s.Rules != nil {
		r = s.Rules(w)
	}
	return []application.BoundSource{{Name: application.SourceForge, Source: s.Source, Rules: r, IncludeIssues: true}}, nil
}
