// Package application holds wakewire's use cases over a watch: register,
// the adapter's hello, next, ack and ended, and the commands that read and
// manage watches. Its ports are defined here; adapters implement them.
package application

import (
	"context"
	"time"

	"github.com/gperezmz/wakewire/internal/watch/domain"
)

// Clock tells the time.
type Clock interface{ Now() time.Time }

// Process is a running program, told apart from a later one under the same
// id by its start time.
type Process struct {
	PID   int
	Start string
}

// Processes reads the process table.
type Processes interface {
	// StartOf answers the start time of pid, and false when no such process runs.
	StartOf(pid int) (string, bool, error)
}

// Repos reads the repository a command runs in.
type Repos interface {
	// Origin answers the repository the `origin` remote of dir points at.
	Origin(ctx context.Context, dir string) (domain.Repo, error)
	// Root answers the top folder of the working tree holding dir.
	Root(ctx context.Context, dir string) (string, error)
	// ReadFile answers a file's text, and false when it does not exist.
	ReadFile(root, name string) (string, bool, error)
}

// Accounts is the account context, as the watch context needs it.
type Accounts interface {
	// ForForge names the account reading a repository and whose it is.
	ForForge(ctx context.Context, repo domain.Repo) (AccountIdentity, error)
	// ForJira names the account reading a Jira site and whose it is.
	ForJira(ctx context.Context, site string) (AccountIdentity, error)
	// Describe answers where an account's token comes from and whether it can write.
	Describe(ctx context.Context, account string) (AccountInfo, error)
}

// AccountInfo is what status shows of an account.
type AccountInfo struct {
	TokenSource string `json:"tokenSource"`
	CanWrite    *bool  `json:"canWrite"` // nil when the service does not say
}

// AccountIdentity is an account and the identity it reads as.
type AccountIdentity struct {
	Account  string
	Service  string // "GitHub", "GitLab", "Jira"
	Identity string // a login, or a Jira account ID
	Display  string // how a person reads the identity
}

// SourceName tells a watch's two halves apart.
type SourceName string

const (
	SourceForge   SourceName = "forge"
	SourceTracker SourceName = "tracker"
)

// RateInfo is a source's remaining budget, as its last response said.
type RateInfo struct {
	Remaining int       `json:"remaining"`
	Limit     int       `json:"limit"`
	ResetAt   time.Time `json:"resetAt"`
	NearLimit bool      `json:"nearLimit,omitempty"`
}

// PollInput is what a source needs for one poll.
type PollInput struct {
	Rules         domain.Rules
	IncludeIssues bool // the tracker is this forge's own issues
	Snapshots     map[string]domain.Snapshot
	State         []byte // the source's own state from its last poll, such as ETags
}

// PollOutput is what one poll of one source read.
type PollOutput struct {
	// Observations hold every item that is new or whose fields or update
	// time moved, each with its timeline; items left out did not move.
	Observations []domain.Observation
	State        []byte
	Rate         RateInfo
	At           time.Time // the service's time
}

// Source reads one service for one watch. Its errors carry the CLI's
// service codes: token_refused, unreachable, rate_limited.
type Source interface {
	Service() string
	Account() string
	Poll(ctx context.Context, in PollInput) (PollOutput, error)
}

// BoundSource is a source as one watch reads it.
type BoundSource struct {
	Name          SourceName
	Source        Source
	Rules         domain.Rules
	IncludeIssues bool
}

// Sources builds the sources a watch reads.
type Sources interface {
	For(ctx context.Context, w Watch) ([]BoundSource, error)
}

// Store is state.db as one unit of work: everything fn does commits
// together or not at all.
type Store interface {
	Tx(ctx context.Context, fn func(Tx) error) error
}

// Tx is one transaction over state.db.
type Tx interface {
	Watches() ([]Watch, error)
	Watch(id int64) (*Watch, error)
	WatchByKey(repo, identity string) (*Watch, error)
	SaveWatch(w *Watch) error
	DeleteWatch(id int64) error

	Registration(watchID int64) (*Registration, error)
	RegistrationBySession(session string) (*Registration, error)
	RegistrationReplacing(session string) (*Registration, error)
	SaveRegistration(r Registration) error

	Adapter(session string) (*Adapter, error)
	AdaptersOf(p Process) ([]Adapter, error)
	SaveAdapter(a Adapter) error
	DeleteAdapter(session string) error

	Snapshots(watchID int64, source SourceName) (map[string]domain.Snapshot, error)
	SaveSnapshot(watchID int64, source SourceName, s domain.Snapshot) error
	DeleteSnapshot(watchID int64, source SourceName, ref string) error

	AddChange(c StoredChange) (int64, error)
	Changes(q ChangeQuery) ([]StoredChange, error)
	SetChangeStatus(ids []int64, status domain.Status, batch int64) error

	OpenBatch(watchID int64) (*Batch, error)
	AddBatch(b *Batch) error
	Batch(id int64) (*Batch, error)
	SaveBatch(b Batch) error

	OpenProblems(watchID int64) ([]Problem, error)
	AddProblem(p *Problem) error
	SaveProblem(p Problem) error

	SourceState(watchID int64, source SourceName) (SourceState, error)
	SaveSourceState(s SourceState) error

	TakeLease(watchID int64, holder Process, until time.Time, free func(Lease) bool) (bool, error)
	ReleaseLease(watchID int64, holder Process) error

	PrunedAt() (time.Time, error)
	Prune(p PrunePlan, dryRun bool) (PruneCounts, error)
}
