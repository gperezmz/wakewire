package github

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gperezmz/wakewire/internal/watch/domain"
)

type user struct {
	Login string `json:"login"`
}

type label struct {
	Name string `json:"name"`
}

// issue is an entry of GET /repos/{o}/{r}/issues: an issue or a pull request.
type issue struct {
	Number      int     `json:"number"`
	Title       string  `json:"title"`
	Body        *string `json:"body"`
	State       string  `json:"state"`
	Labels      []label `json:"labels"`
	Assignees   []user  `json:"assignees"`
	User        user    `json:"user"`
	Comments    int     `json:"comments"`
	UpdatedAt   string  `json:"updated_at"`
	HTMLURL     string  `json:"html_url"`
	PullRequest *struct {
		MergedAt *string `json:"merged_at"`
	} `json:"pull_request"`
}

func (i issue) ref() string {
	if i.PullRequest != nil {
		return "PR #" + itoa(i.Number)
	}
	return "#" + itoa(i.Number)
}

func (i issue) item(head string) domain.Item {
	it := domain.Item{Ref: i.ref(), Kind: domain.ItemIssue, URL: i.HTMLURL, State: i.State, Author: i.User.Login,
		Title: i.Title, Comments: i.Comments, UpdatedAt: i.UpdatedAt, Labels: []string{}, Assignees: []string{}}
	if i.Body != nil {
		it.Body = *i.Body
	}
	for _, l := range i.Labels {
		it.Labels = append(it.Labels, l.Name)
	}
	for _, a := range i.Assignees {
		it.Assignees = append(it.Assignees, a.Login)
	}
	slices.Sort(it.Labels)
	slices.Sort(it.Assignees)
	if i.PullRequest != nil {
		it.Kind, it.Head = domain.ItemPull, head
		if i.PullRequest.MergedAt != nil {
			it.MergedAt = *i.PullRequest.MergedAt
		}
	}
	return it
}

// pull is an entry of GET /repos/{o}/{r}/pulls.
type pull struct {
	Number int `json:"number"`
	Head   struct {
		SHA string `json:"sha"`
	} `json:"head"`
}

// event is an entry of GET /repos/{o}/{r}/issues/{n}/timeline.
type event struct {
	ID          int64   `json:"id"`
	Event       string  `json:"event"`
	Actor       *user   `json:"actor"`
	User        *user   `json:"user"`
	Assignee    *user   `json:"assignee"`
	Label       *label  `json:"label"`
	CreatedAt   string  `json:"created_at"`
	SubmittedAt string  `json:"submitted_at"`
	Body        *string `json:"body"`
	State       string  `json:"state"`
	HTMLURL     string  `json:"html_url"`
}

func (e event) by() string {
	switch {
	case e.User != nil && (e.Event == "commented" || e.Event == "reviewed"):
		return e.User.Login
	case e.Actor != nil:
		return e.Actor.Login
	}
	return ""
}

func (e event) activity() (domain.Activity, bool) {
	at := parseTime(e.CreatedAt)
	if e.Event == "reviewed" {
		at = parseTime(e.SubmittedAt)
	}
	a := domain.Activity{ID: "ev:" + itoa64(e.ID), By: e.by(), At: at, URL: e.HTMLURL}
	if e.Body != nil {
		a.Body = *e.Body
	}
	switch e.Event {
	case "labeled", "unlabeled":
		if e.Label == nil {
			return a, false
		}
		a.Kind, a.Label = domain.KindLabelAdded, e.Label.Name
		if e.Event == "unlabeled" {
			a.Kind = domain.KindLabelRemoved
		}
	case "assigned", "unassigned":
		a.Kind = domain.KindReassigned
		if e.Assignee != nil {
			a.Assignee = e.Assignee.Login
		}
	case "closed":
		a.Kind = domain.KindClosed
	case "reopened":
		a.Kind = domain.KindReopened
	case "merged":
		a.Kind = domain.KindMerged
	case "renamed":
		a.Kind, a.Field = domain.KindEdited, "title"
	case "commented":
		a.Kind = domain.KindComment
	case "reviewed":
		a.Kind, a.State = domain.KindReview, strings.ToLower(e.State)
		// A review of state "commented" with no body only carries inline
		// comments, which are read on their own.
		if a.State == "commented" && strings.TrimSpace(a.Body) == "" {
			return a, false
		}
	default:
		return a, false
	}
	if e.ID == 0 {
		return a, false
	}
	return a, true
}

// reviewComment is an entry of GET /repos/{o}/{r}/pulls/{n}/comments.
type reviewComment struct {
	ID           int64  `json:"id"`
	User         user   `json:"user"`
	Body         string `json:"body"`
	Path         string `json:"path"`
	Line         *int   `json:"line"`
	OriginalLine *int   `json:"original_line"`
	DiffHunk     string `json:"diff_hunk"`
	CreatedAt    string `json:"created_at"`
	HTMLURL      string `json:"html_url"`
}

func (c reviewComment) activity() domain.Activity {
	line := 0
	switch {
	case c.Line != nil:
		line = *c.Line
	case c.OriginalLine != nil:
		line = *c.OriginalLine
	}
	return domain.Activity{ID: "rc:" + itoa64(c.ID), Kind: domain.KindInlineComment, By: c.User.Login, At: parseTime(c.CreatedAt),
		URL: c.HTMLURL, Body: c.Body, Path: c.Path, Line: line, Hunk: c.DiffHunk}
}

// commit is an entry of GET /repos/{o}/{r}/pulls/{n}/commits.
type commit struct {
	SHA    string `json:"sha"`
	Author *user  `json:"author"`
	Commit struct {
		Author struct {
			Email string `json:"email"`
		} `json:"author"`
	} `json:"commit"`
}

func (c commit) author() string {
	if c.Author != nil && c.Author.Login != "" {
		return c.Author.Login
	}
	return c.Commit.Author.Email
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t.UTC()
}

func itoa(n int) string { return strconv.Itoa(n) }

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }
