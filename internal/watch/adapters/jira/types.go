package jira

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gperezmz/wakewire/internal/watch/domain"
)

// person is a user as Jira names one. People are told apart by accountId.
type person struct {
	AccountID   string `json:"accountId"`
	DisplayName string `json:"displayName"`
}

// searchPage is the answer to POST /rest/api/3/search/jql.
type searchPage struct {
	Issues        []issue `json:"issues"`
	NextPageToken string  `json:"nextPageToken"`
	IsLast        *bool   `json:"isLast"`
}

// issue is an entry of the search's issues.
type issue struct {
	ID     string `json:"id"`
	Key    string `json:"key"`
	Fields struct {
		Summary     string          `json:"summary"`
		Description json.RawMessage `json:"description"`
		Labels      []string        `json:"labels"`
		Assignee    *person         `json:"assignee"`
		Reporter    *person         `json:"reporter"`
		Status      *struct {
			ID             string `json:"id"`
			Name           string `json:"name"`
			StatusCategory struct {
				Key string `json:"key"`
			} `json:"statusCategory"`
		} `json:"status"`
		Resolution *struct {
			Name string `json:"name"`
		} `json:"resolution"`
		Comment *struct {
			Total    *int              `json:"total"`
			Comments []json.RawMessage `json:"comments"`
		} `json:"comment"`
		Updated string `json:"updated"`
	} `json:"fields"`
}

// category is the status category of the issue's status.
func (i issue) category() string {
	if i.Fields.Status == nil {
		return ""
	}
	return i.Fields.Status.StatusCategory.Key
}

// item builds the domain item. site is the Jira site, for the browse link.
func (i issue) item(site string) domain.Item {
	f := i.Fields
	it := domain.Item{Ref: i.Key, Kind: domain.ItemIssue, URL: browseURL(site, i.Key), State: "open",
		Labels: []string{}, Assignees: []string{}, Title: f.Summary, Body: markdownOf(f.Description),
		Comments: -1, UpdatedAt: f.Updated}
	if i.category() == "done" {
		it.State = "closed"
	}
	it.Labels = append(it.Labels, f.Labels...)
	slices.Sort(it.Labels)
	if f.Assignee != nil && f.Assignee.AccountID != "" {
		it.Assignees = append(it.Assignees, f.Assignee.AccountID)
	}
	if f.Reporter != nil {
		it.Author = f.Reporter.AccountID
	}
	if c := f.Comment; c != nil {
		switch {
		case c.Total != nil:
			it.Comments = *c.Total
		case c.Comments != nil:
			it.Comments = len(c.Comments)
		}
	}
	return it
}

func browseURL(site, key string) string { return "https://" + site + "/browse/" + key }

// changelogItem is one field a history changed.
type changelogItem struct {
	Field      string  `json:"field"`
	FieldID    string  `json:"fieldId"`
	From       *string `json:"from"`
	FromString *string `json:"fromString"`
	To         *string `json:"to"`
	ToString   *string `json:"toString"`
}

func (c changelogItem) name() string {
	if c.FieldID != "" {
		return strings.ToLower(c.FieldID)
	}
	return strings.ToLower(c.Field)
}

// history is one edit: who made it, when, and the fields it changed.
type history struct {
	ID      string          `json:"id"`
	Author  *person         `json:"author"`
	Created string          `json:"created"`
	Items   []changelogItem `json:"items"`
}

// activities turns one history into the activities its items show. Ids
// are cl:<history>:<n>, n counting the activities the history gave; the
// same history always gives the same ids. statuses maps a status id to its
// category, as far as it is known.
func (h history) activities(statuses map[string]string) []domain.Activity {
	by := ""
	if h.Author != nil {
		by = h.Author.DisplayName
	}
	at := parseTime(h.Created)
	var out []domain.Activity
	add := func(a domain.Activity) {
		a.ID = "cl:" + h.ID + ":" + itoa(len(out))
		a.By, a.At = by, at
		out = append(out, a)
	}
	for _, it := range h.Items {
		switch it.name() {
		case "labels":
			// One item may hold every label joined by spaces, or one item
			// each label: comparing the two sets reads both.
			before, after := strings.Fields(deref(it.FromString)), strings.Fields(deref(it.ToString))
			for _, l := range after {
				if !slices.Contains(before, l) {
					add(domain.Activity{Kind: domain.KindLabelAdded, Label: l})
				}
			}
			for _, l := range before {
				if !slices.Contains(after, l) {
					add(domain.Activity{Kind: domain.KindLabelRemoved, Label: l})
				}
			}
		case "assignee":
			add(domain.Activity{Kind: domain.KindReassigned, Assignee: deref(it.To)})
		case "status":
			from, to := statusCategory(statuses, it.From, it.FromString), statusCategory(statuses, it.To, it.ToString)
			switch {
			case to == "done" && from != "done":
				add(domain.Activity{Kind: domain.KindClosed})
			case from == "done" && to != "" && to != "done":
				add(domain.Activity{Kind: domain.KindReopened})
			}
		case "summary":
			add(domain.Activity{Kind: domain.KindEdited, Field: "title"})
		case "description":
			add(domain.Activity{Kind: domain.KindEdited, Field: "description"})
		}
	}
	return out
}

// statusCategory finds the category of a status the changelog names by id
// and by name; statuses holds both, the names under a "name:" prefix.
func statusCategory(statuses map[string]string, id, name *string) string {
	if c := statuses[deref(id)]; c != "" {
		return c
	}
	return statuses["name:"+deref(name)]
}

// comment is an entry of GET /rest/api/3/issue/{key}/comment.
type comment struct {
	ID      string          `json:"id"`
	Author  *person         `json:"author"`
	Body    json.RawMessage `json:"body"`
	Created string          `json:"created"`
}

func (c comment) activity(site, key string) domain.Activity {
	a := domain.Activity{ID: "c:" + c.ID, Kind: domain.KindComment, At: parseTime(c.Created),
		URL: browseURL(site, key) + "?focusedCommentId=" + c.ID, Body: markdownOf(c.Body)}
	if c.Author != nil {
		a.By = c.Author.DisplayName
	}
	return a
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// parseTime reads Jira's timestamps, which end in an offset without a
// colon (+0000), and RFC 3339.
func parseTime(s string) time.Time {
	for _, layout := range []string{"2006-01-02T15:04:05.000-0700", "2006-01-02T15:04:05-0700", time.RFC3339Nano} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func itoa(n int) string { return strconv.Itoa(n) }
