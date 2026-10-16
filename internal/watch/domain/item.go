package domain

import (
	"slices"
	"strings"
	"time"
)

// ItemKind tells a spec's item from a pull request.
type ItemKind string

const (
	ItemIssue ItemKind = "issue"
	ItemPull  ItemKind = "pull"
)

// Item is what a source shows of one item now: the fields a poll diffs.
type Item struct {
	Ref       string   `json:"ref"` // "#12", "PR #41", "!41", "PROJ-12"
	Kind      ItemKind `json:"kind"`
	URL       string   `json:"url"`
	State     string   `json:"state"` // "open" or "closed"
	Labels    []string `json:"labels"`
	Assignees []string `json:"assignees"`
	Author    string   `json:"author"`
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	Comments  int      `json:"comments"` // the service's count; -1 when it gives none
	MergedAt  string   `json:"mergedAt,omitempty"`
	Head      string   `json:"head,omitempty"`
	UpdatedAt string   `json:"updatedAt"`
}

// Activity is one timeline entry a source read for an item: a comment, a
// review, a label event, a commit. Its ID is unique within the item.
type Activity struct {
	ID       string
	Kind     Kind
	By       string
	At       time.Time
	URL      string
	Body     string
	State    string // a review's state
	Path     string
	Line     int
	Hunk     string
	Label    string
	Field    string // "title" or "description" for an edit
	Commit   *Commit
	Assignee string // for an assignment event: who was assigned or unassigned
}

// Rules are what a watch needs to judge a source's changes.
type Rules struct {
	Identity string   // the identity on this source: a login, or a Jira account ID
	Aliases  []string // other names commits carry for the identity, such as its emails
	Labels   Labels
	Marker   string
}

// IsIdentity says whether name is the identity or one of its aliases.
func (r Rules) IsIdentity(name string) bool {
	if name == "" {
		return false
	}
	if strings.EqualFold(name, r.Identity) {
		return true
	}
	return slices.ContainsFunc(r.Aliases, func(a string) bool { return strings.EqualFold(a, name) })
}

// InSet says whether an item is in the watch's sets: a spec in the
// identity's partition, or a pull request the identity opened.
func (r Rules) InSet(it Item) bool {
	if it.Kind == ItemPull {
		return r.IsIdentity(it.Author)
	}
	if !hasLabel(it.Labels, r.Labels[MeaningSpec]) {
		return false
	}
	if len(it.Assignees) == 0 {
		return r.IsIdentity(it.Author)
	}
	return slices.ContainsFunc(it.Assignees, r.IsIdentity)
}

// assignedTo says whether an assignment activity gave the item to the
// identity: it names the identity (a service that does not say whom it
// assigned leaves it empty), and the item shows the identity assigned.
func (r Rules) assignedTo(it Item, a Activity) bool {
	return (a.Assignee == "" || r.IsIdentity(a.Assignee)) && slices.ContainsFunc(it.Assignees, r.IsIdentity)
}

func hasLabel(labels []string, want string) bool {
	return want != "" && slices.ContainsFunc(labels, func(l string) bool { return strings.EqualFold(l, want) })
}

// labelDiff answers the labels added to and removed from before.
func labelDiff(before, after []string) (added, removed []string) {
	for _, l := range after {
		if !hasLabel(before, l) {
			added = append(added, l)
		}
	}
	for _, l := range before {
		if !hasLabel(after, l) {
			removed = append(removed, l)
		}
	}
	return added, removed
}

func sameSet(a, b []string) bool {
	x, y := slices.Clone(a), slices.Clone(b)
	for i := range x {
		x[i] = strings.ToLower(x[i])
	}
	for i := range y {
		y[i] = strings.ToLower(y[i])
	}
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(slices.Compact(x), slices.Compact(y))
}
