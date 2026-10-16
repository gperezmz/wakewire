package domain

import (
	"fmt"
	"regexp"
	"strings"
)

// Repo is a repository on a forge, as `<host>/<path>`.
type Repo struct {
	Host string
	Path string // "owner/repo", or "group/sub/project" on GitLab
}

func (r Repo) String() string { return r.Host + "/" + r.Path }

// Owner is the first segment of the path: the organization, user or group
// whose account reads the repository.
func (r Repo) Owner() string {
	owner, _, _ := strings.Cut(r.Path, "/")
	return owner
}

// ParseRepo reads `<host>/<owner>/<repo>[/...]`.
func ParseRepo(s string) (Repo, error) {
	s = strings.Trim(strings.TrimSpace(s), "/")
	s = strings.TrimSuffix(s, ".git")
	host, path, ok := strings.Cut(s, "/")
	if !ok || host == "" || !strings.Contains(path, "/") || strings.Contains(host, ":") {
		return Repo{}, Invalid(fmt.Sprintf("%q is not a repository as <host>/<owner>/<repo>", s),
			"pass --repo github.com/<owner>/<repo>")
	}
	return Repo{Host: strings.ToLower(host), Path: path}, nil
}

// TrackerKind names where a watch's specs live.
type TrackerKind string

const (
	TrackerGitHub TrackerKind = "github-issues"
	TrackerGitLab TrackerKind = "gitlab-issues"
	TrackerJira   TrackerKind = "jira"
)

// Tracker is a parsed tracker line.
type Tracker struct {
	Kind    TrackerKind
	Site    string // Jira only
	Project string // Jira only: the project key
}

func (t Tracker) String() string {
	if t.Kind == TrackerJira {
		return "jira:" + t.Site + "/" + t.Project
	}
	return string(t.Kind)
}

var jiraKey = regexp.MustCompile(`^[A-Z][A-Z0-9_]+$`)

// ParseTracker reads `github-issues`, `gitlab-issues` or `jira:<site>/<KEY>`.
func ParseTracker(s string) (Tracker, error) {
	s = strings.Trim(strings.TrimSpace(s), "`")
	switch {
	case s == string(TrackerGitHub):
		return Tracker{Kind: TrackerGitHub}, nil
	case s == string(TrackerGitLab):
		return Tracker{Kind: TrackerGitLab}, nil
	case strings.HasPrefix(s, "jira:"):
		site, key, ok := strings.Cut(strings.TrimPrefix(s, "jira:"), "/")
		if ok && site != "" && jiraKey.MatchString(key) {
			return Tracker{Kind: TrackerJira, Site: strings.ToLower(site), Project: key}, nil
		}
	}
	return Tracker{}, Invalid(fmt.Sprintf("%q is not a tracker", s),
		"use github-issues, gitlab-issues or jira:<site>/<PROJECT KEY>")
}

// Meaning is what a label means to wakewire.
type Meaning string

const (
	MeaningSpec       Meaning = "spec"
	MeaningReady      Meaning = "ready"
	MeaningInProgress Meaning = "in-progress"
	MeaningNeedsInput Meaning = "needs-input"
)

// Meanings are every meaning, in the order a person reads them.
var Meanings = []Meaning{MeaningSpec, MeaningReady, MeaningInProgress, MeaningNeedsInput}

// Labels maps each meaning to the repository's label for it.
type Labels map[Meaning]string

// Missing lists the meanings with no label.
func (l Labels) Missing() []Meaning {
	var out []Meaning
	for _, m := range Meanings {
		if l[m] == "" {
			out = append(out, m)
		}
	}
	return out
}

// Is says whether label is the one for m, without regard to case.
func (l Labels) Is(label string, m Meaning) bool {
	return l[m] != "" && strings.EqualFold(l[m], label)
}

// ParseLabelFlag reads `<meaning>=<label>`.
func ParseLabelFlag(s string) (Meaning, string, error) {
	m, label, ok := strings.Cut(s, "=")
	m = strings.ReplaceAll(strings.TrimSpace(strings.ToLower(m)), "_", "-")
	label = strings.Trim(strings.TrimSpace(label), "`")
	if ok && label != "" {
		for _, known := range Meanings {
			if Meaning(m) == known {
				return known, label, nil
			}
		}
	}
	return "", "", Invalid(fmt.Sprintf("--label %q is not <meaning>=<label>", s),
		"meanings are spec, ready, in-progress and needs-input, as --label ready=ready-for-dev")
}
