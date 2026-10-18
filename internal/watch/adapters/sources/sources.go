// Package sources builds the sources a watch reads, each with the
// credential of the account it reads through.
package sources

import (
	"context"

	"github.com/gperezmz/wakewire/v2/internal/adapters/httpread"
	"github.com/gperezmz/wakewire/v2/internal/watch/adapters/accounts"
	"github.com/gperezmz/wakewire/v2/internal/watch/adapters/github"
	"github.com/gperezmz/wakewire/v2/internal/watch/adapters/gitlab"
	"github.com/gperezmz/wakewire/v2/internal/watch/adapters/jira"
	"github.com/gperezmz/wakewire/v2/internal/watch/application"
	"github.com/gperezmz/wakewire/v2/internal/watch/domain"
)

// Endpoints are the services' roots, replaced in tests.
type Endpoints struct {
	GitHub string                   // the GitHub API root
	Origin func(host string) string // scheme and host of a GitLab or Jira host
}

// Sources binds a watch to its sources.
type Sources struct {
	Accounts  accounts.Accounts
	HTTP      *httpread.Client
	Endpoints Endpoints
}

// For answers the forge source, and the Jira source where the tracker is
// Jira.
// Each half reads through the account that covers it now, found as register
// finds it, by host and owner or by Jira site, so a renamed account keeps
// its watches.
func (s Sources) For(_ context.Context, w application.Watch) ([]application.BoundSource, error) {
	cred, err := s.Accounts.Service.ForRepo(w.Repo.Host, w.Repo.Owner())
	if err != nil {
		return nil, err
	}
	forgeRules := domain.Rules{Identity: w.Identity, Labels: w.Labels, Marker: w.Marker}
	// ForRepo answers only an account that covers a repository, which is
	// never a Jira one, so the forge is GitHub or GitLab.
	var forge application.Source = &github.Source{HTTP: s.HTTP, Base: s.Endpoints.GitHub, Token: cred.Token, Repo: w.Repo, Name: cred.Account}
	if accounts.ServiceName(cred.Service) == "GitLab" {
		origin := "https://" + w.Repo.Host
		if s.Endpoints.Origin != nil {
			origin = s.Endpoints.Origin(w.Repo.Host)
		}
		forge = &gitlab.Source{HTTP: s.HTTP, Base: origin, Token: cred.Token, Repo: w.Repo, Name: cred.Account}
	}
	out := []application.BoundSource{{Name: application.SourceForge, Source: forge, Rules: forgeRules,
		IncludeIssues: w.Tracker.Kind != domain.TrackerJira}}
	if w.Tracker.Kind == domain.TrackerJira {
		tc, err := s.Accounts.Service.ForSite(w.Tracker.Site)
		if err != nil {
			// The forge still polls: the tracker's half fails alone, as a
			// problem on the tracker.
			out = append(out, application.BoundSource{Name: application.SourceTracker, Source: unavailable{service: "Jira", err: err}})
			return out, nil
		}
		base := "https://" + w.Tracker.Site
		if s.Endpoints.Origin != nil {
			base = s.Endpoints.Origin(w.Tracker.Site)
		}
		out = append(out, application.BoundSource{Name: application.SourceTracker,
			Source: &jira.Source{HTTP: s.HTTP, Base: base, Site: w.Tracker.Site, Project: w.Tracker.Project,
				Email: tc.Email, Token: tc.Token, Name: tc.Account},
			Rules: domain.Rules{Identity: w.TrackerIdentity, Labels: w.Labels, Marker: w.Marker}})
	}
	return out, nil
}

// unavailable stands for a source whose account cannot be found: each poll
// answers the reason, so the problem is the source's own.
type unavailable struct {
	service string
	err     error
}

func (u unavailable) Service() string { return u.service }
func (u unavailable) Account() string { return "" }
func (u unavailable) Poll(context.Context, application.PollInput) (application.PollOutput, error) {
	return application.PollOutput{}, u.err
}
