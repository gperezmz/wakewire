package gitlab

import (
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gperezmz/wakewire/v2/internal/watch/domain"
)

type user struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

// account is GET /user: the token's owner, whose emails git commits carry.
type account struct {
	Username    string `json:"username"`
	Email       string `json:"email"`
	CommitEmail string `json:"commit_email"`
	PublicEmail string `json:"public_email"`
}

func (a account) hasEmail(e string) bool {
	return e != "" && (strings.EqualFold(e, a.Email) || strings.EqualFold(e, a.CommitEmail) || strings.EqualFold(e, a.PublicEmail))
}

// entry is an issue or a merge request, as the lists and the single reads
// give it; the fields only a merge request has stay empty on an issue.
type entry struct {
	IID            int      `json:"iid"`
	Title          string   `json:"title"`
	Description    *string  `json:"description"`
	State          string   `json:"state"`
	Labels         []string `json:"labels"`
	Assignees      []user   `json:"assignees"`
	Author         user     `json:"author"`
	UserNotesCount int      `json:"user_notes_count"`
	UpdatedAt      string   `json:"updated_at"`
	WebURL         string   `json:"web_url"`
	SHA            string   `json:"sha"`
	MergedAt       *string  `json:"merged_at"`
}

func (e entry) item(kind domain.ItemKind) domain.Item {
	ref := "#" + strconv.Itoa(e.IID)
	if kind == domain.ItemPull {
		ref = "!" + strconv.Itoa(e.IID)
	}
	it := domain.Item{Ref: ref, Kind: kind, URL: e.WebURL, State: "open", Author: e.Author.Username, Title: e.Title,
		Comments: e.UserNotesCount, UpdatedAt: e.UpdatedAt, Labels: []string{}, Assignees: []string{}}
	// GitLab's states: opened, closed, locked (a merge request being
	// merged) and merged; all but the first are closed.
	if e.State != "opened" {
		it.State = "closed"
	}
	if e.Description != nil {
		it.Body = *e.Description
	}
	it.Labels = append(it.Labels, e.Labels...)
	for _, a := range e.Assignees {
		it.Assignees = append(it.Assignees, a.Username)
	}
	slices.Sort(it.Labels)
	slices.Sort(it.Assignees)
	if kind == domain.ItemPull {
		it.Head = e.SHA
		if e.MergedAt != nil {
			it.MergedAt = *e.MergedAt
		}
	}
	return it
}

// labelEvent is an entry of .../resource_label_events.
type labelEvent struct {
	ID        int64  `json:"id"`
	User      *user  `json:"user"`
	CreatedAt string `json:"created_at"`
	Action    string `json:"action"`
	Label     *struct {
		Name string `json:"name"`
	} `json:"label"`
}

func (e labelEvent) activity() (domain.Activity, bool) {
	if e.Label == nil {
		return domain.Activity{}, false // the label has since been deleted
	}
	a := domain.Activity{ID: "le:" + itoa64(e.ID), Kind: domain.KindLabelAdded, By: byOf(e.User), At: parseTime(e.CreatedAt), Label: e.Label.Name}
	switch e.Action {
	case "add":
	case "remove":
		a.Kind = domain.KindLabelRemoved
	default:
		return a, false
	}
	return a, true
}

// stateEvent is an entry of .../resource_state_events.
type stateEvent struct {
	ID        int64  `json:"id"`
	User      *user  `json:"user"`
	CreatedAt string `json:"created_at"`
	State     string `json:"state"`
}

func (e stateEvent) activity() (domain.Activity, bool) {
	a := domain.Activity{ID: "se:" + itoa64(e.ID), By: byOf(e.User), At: parseTime(e.CreatedAt)}
	switch e.State {
	case "closed":
		a.Kind = domain.KindClosed
	case "reopened":
		a.Kind = domain.KindReopened
	case "merged":
		a.Kind = domain.KindMerged
	default:
		return a, false
	}
	return a, true
}

// note is an entry of .../notes: a comment, or a system note GitLab wrote
// about a change.
type note struct {
	ID        int64   `json:"id"`
	Type      *string `json:"type"`
	Body      string  `json:"body"`
	Author    user    `json:"author"`
	CreatedAt string  `json:"created_at"`
	System    bool    `json:"system"`
	Position  *struct {
		NewPath string `json:"new_path"`
		OldPath string `json:"old_path"`
		NewLine *int   `json:"new_line"`
		OldLine *int   `json:"old_line"`
	} `json:"position"`
}

func (n note) activity(itemURL string) (domain.Activity, bool) {
	a := domain.Activity{ID: "n:" + itoa64(n.ID), By: n.Author.Username, At: parseTime(n.CreatedAt), URL: itemURL + "#note_" + itoa64(n.ID)}
	if n.System {
		// The system notes that say who edited or reassigned an item: the
		// fields show that it changed, and these show who did it.
		switch {
		case n.Body == "changed the description":
			a.Kind, a.Field = domain.KindEdited, "description"
		case strings.HasPrefix(n.Body, "changed title from"):
			a.Kind, a.Field = domain.KindEdited, "title"
		case strings.HasPrefix(n.Body, "assigned to @"), strings.HasPrefix(n.Body, "unassigned @"):
			a.Kind = domain.KindReassigned
		default:
			return a, false
		}
		return a, true
	}
	a.Body = n.Body
	a.Kind = domain.KindComment
	if n.Position != nil {
		a.Kind, a.Path = domain.KindInlineComment, n.Position.NewPath
		switch {
		case n.Position.NewLine != nil:
			a.Line = *n.Position.NewLine
		case n.Position.OldLine != nil:
			a.Line = *n.Position.OldLine
		}
		if a.Path == "" {
			a.Path = n.Position.OldPath
		}
	}
	return a, true
}

// approvedNote is the system note GitLab writes when someone approves a
// merge request; the approvals list does not say when.
const approvedNote = "approved this merge request"

// approvals is GET .../merge_requests/:iid/approvals.
type approvals struct {
	ApprovedBy []struct {
		User       user   `json:"user"`
		ApprovedAt string `json:"approved_at"`
	} `json:"approved_by"`
}

// commit is an entry of GET .../merge_requests/:iid/commits.
type commit struct {
	ID          string `json:"id"`
	AuthorEmail string `json:"author_email"`
}

func byOf(u *user) string {
	if u == nil {
		return ""
	}
	return u.Username
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t.UTC()
}

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }
