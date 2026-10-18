package domain

import "time"

// Kind is what kind of change was found; the wake contract lists them.
type Kind string

const (
	KindLabelAdded    Kind = "label_added"
	KindLabelRemoved  Kind = "label_removed"
	KindComment       Kind = "comment"
	KindInlineComment Kind = "inline_comment"
	KindReview        Kind = "review"
	KindClosed        Kind = "closed"
	KindReopened      Kind = "reopened"
	KindMerged        Kind = "merged"
	KindReassigned    Kind = "reassigned"
	KindEdited        Kind = "edited"
	KindPushed        Kind = "pushed"
	KindProblem       Kind = "problem"
)

// Status is what was done with a change; the ledger shows it.
type Status string

const (
	StatusPending   Status = "pending"
	StatusHeld      Status = "held"
	StatusDelivered Status = "delivered"
	StatusIgnored   Status = "ignored"
)

// Commit is one commit a push brought.
type Commit struct {
	ID     string `json:"id"`
	Author string `json:"author"`
}

// Change is one difference a poll found on one item. Its JSON is the
// batch's change format; see docs/reference/wake-contract.md.
type Change struct {
	Item      string    `json:"item"`
	URL       string    `json:"url"`
	Kind      Kind      `json:"kind"`
	By        string    `json:"by,omitempty"`
	At        time.Time `json:"at"`
	Body      *string   `json:"body,omitempty"`
	Truncated *bool     `json:"truncated,omitempty"` // read only: true on a body stored cut, which no poll stores now
	State     string    `json:"state,omitempty"`
	Path      string    `json:"path,omitempty"`
	Line      int       `json:"line,omitempty"`
	Hunk      string    `json:"hunk,omitempty"`
	Label     string    `json:"label,omitempty"`
	From      []string  `json:"from,omitempty"`
	To        []string  `json:"to,omitempty"`
	Field     string    `json:"field,omitempty"`
	Diff      string    `json:"diff,omitempty"`
	Head      string    `json:"head,omitempty"`
	Commits   []Commit  `json:"commits,omitempty"`
	Code      string    `json:"code,omitempty"`
	Text      string    `json:"text,omitempty"`

	// Not part of the batch format: the ledger's own columns.
	Status Status `json:"-"`
	Reason string `json:"-"`
}

// WithBody sets body, whole: the services cap their own texts, and the
// daily clean-up bounds how long one is kept.
func (c Change) WithBody(body string) Change {
	c.Body = &body
	return c
}
