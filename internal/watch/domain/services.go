package domain

import (
	"fmt"
	"strings"
)

// CheckForge holds the Services table's forge row to the repository
// watched, on a forge whose service is forgeService ("GitHub" or
// "GitLab"). A file with no forge row passes.
func CheckForge(r SourceRead, repo Repo, forgeService string) error {
	row := r.ForgeRow
	if row.Line == 0 {
		return nil
	}
	if strings.EqualFold(row.Service, "none") {
		return Refuse("source_incomplete",
			fmt.Sprintf("the forge row of %s (line %d) names no forge, and wakewire watches one", r.File, row.Line),
			"name the forge in that row, or pass --repo <host>/<owner>/<repo>")
	}
	if !strings.EqualFold(row.Service, forgeService) || !strings.EqualFold(row.Scope, repo.Path) {
		return Refuse("source_incomplete",
			fmt.Sprintf("the forge row of %s (line %d) is %s `%s`, but the repository is %s on %s",
				r.File, row.Line, row.Service, row.Scope, repo.Path, forgeService),
			"fix that row, or pass --repo <host>/<owner>/<repo>")
	}
	return nil
}

// TrackerFrom reads the tracker from the Services table's tracker row, for
// the repository watched on a forge whose service is forgeService. A
// forge's issues are read only on the repository watched.
func TrackerFrom(r SourceRead, repo Repo, forgeService string) (Tracker, error) {
	row := r.TrackerRow
	where := fmt.Sprintf("the tracker row of %s (line %d)", r.File, row.Line)
	hint := "fix that row, or pass --tracker github-issues, gitlab-issues or jira:<site>/<KEY>"
	switch service := strings.ToLower(row.Service); service {
	case "github", "gitlab":
		kind := map[string]TrackerKind{"github": TrackerGitHub, "gitlab": TrackerGitLab}[service]
		if !strings.EqualFold(row.Service, forgeService) || !strings.EqualFold(row.Scope, repo.Path) {
			return Tracker{}, Refuse("source_incomplete",
				fmt.Sprintf("%s is %s `%s`, and wakewire reads a forge's issues only on the repository watched, %s on %s",
					where, row.Service, row.Scope, repo.Path, forgeService), hint)
		}
		return Tracker{Kind: kind}, nil
	case "jira":
		t, err := ParseTracker("jira:" + row.Scope)
		if err != nil {
			return Tracker{}, Refuse("source_incomplete",
				fmt.Sprintf("%s is Jira `%s`, which is not <site>/<PROJECT KEY>", where, row.Scope), hint)
		}
		return t, nil
	default:
		return Tracker{}, Refuse("source_incomplete",
			fmt.Sprintf("%s is %s, and wakewire reads specs from GitHub, GitLab or Jira", where, row.Service), hint)
	}
}
