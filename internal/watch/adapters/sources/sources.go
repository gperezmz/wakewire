// Package sources builds the sources a watch reads, each with the
// credential of the account it reads through.
package sources

import (
	"context"
	"fmt"

	"github.com/gperezmz/wakewire/internal/adapters/httpread"
	"github.com/gperezmz/wakewire/internal/watch/adapters/accounts"
	"github.com/gperezmz/wakewire/internal/watch/adapters/github"
	"github.com/gperezmz/wakewire/internal/watch/application"
	"github.com/gperezmz/wakewire/internal/watch/domain"
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
func (s Sources) For(_ context.Context, w application.Watch) ([]application.BoundSource, error) {
	cred, err := s.Accounts.Credential(w.ForgeAccount)
	if err != nil {
		return nil, err
	}
	forgeRules := domain.Rules{Identity: w.Identity, Labels: w.Labels, Marker: w.Marker}
	var forge application.Source
	switch accounts.ServiceName(cred.Service) {
	case "GitHub":
		forge = &github.Source{HTTP: s.HTTP, Base: s.Endpoints.GitHub, Token: cred.Token, Repo: w.Repo, Name: cred.Account}
	default:
		return nil, domain.Invalid(fmt.Sprintf("%s has no source yet", accounts.ServiceName(cred.Service)), "see docs/reference/sources.md")
	}
	out := []application.BoundSource{{Name: application.SourceForge, Source: forge, Rules: forgeRules,
		IncludeIssues: w.Tracker.Kind != domain.TrackerJira}}
	return out, nil
}
