package application

import (
	"time"

	"github.com/gperezmz/wakewire/v2/internal/watch/domain"
)

// Watch is what wakewire keeps an eye on for one identity in one repository.
type Watch struct {
	ID              int64
	Repo            domain.Repo
	Identity        string // the forge identity: half of the watch's key
	ForgeAccount    string
	Tracker         domain.Tracker
	TrackerIdentity string
	TrackerAccount  string
	Labels          domain.Labels
	Marker          string
	Interval        time.Duration // 0: the configuration's
	PausedSince     *time.Time
	PausedUntil     *time.Time
	PolledAt        time.Time // zero until the first poll
	CreatedAt       time.Time
}

// Registration is the record that one session holds a watch.
type Registration struct {
	WatchID      int64
	Harness      string
	Session      string
	Process      Process
	RegisteredAt time.Time
	HeardAt      time.Time
	EndedAt      *time.Time
	EndReason    string
	Replaced     string // the session this one took the watch over from
}

// Adapter is one session's loaded adapter.
type Adapter struct {
	Session  string
	Process  Process
	HelloAt  time.Time
	HeardAt  time.Time
	Protocol int // the wake contract the adapter speaks
}

// StoredChange is a change as the ledger keeps it.
type StoredChange struct {
	ID      int64
	WatchID int64
	Source  SourceName
	Change  domain.Change
	FoundAt time.Time
	Batch   int64 // the batch that delivered it, or that last offered it
}

// ChangeQuery selects ledger rows.
type ChangeQuery struct {
	WatchID int64
	Status  []domain.Status // empty: any
	Since   time.Time
	Item    string
	Batch   int64
	IDs     []int64
}

// Batch is the changes one wake reports.
type Batch struct {
	ID        int64
	WatchID   int64
	Changes   []int64
	OfferedAt time.Time
	AckedAt   *time.Time
}

// Problem stops a watch from seeing one of its sources.
type Problem struct {
	ID          int64
	WatchID     int64
	Source      SourceName
	Code        string
	Text        string
	Hint        string // the command or setting that fixes it
	Since       time.Time
	DeliveredAt *time.Time
	ClearedAt   *time.Time
}

// SourceState is what a source keeps between polls.
type SourceState struct {
	WatchID   int64
	Source    SourceName
	Data      []byte // the source adapter's own, such as ETags
	Failures  int    // unreachable polls in a row
	Rate      RateInfo
	SkipUntil time.Time
	OKAt      time.Time
}

// Lease is one process's claim to be polling a watch now.
type Lease struct {
	WatchID   int64
	Process   Process
	ExpiresAt time.Time
}

// PrunePlan says what the daily clean-up removes.
type PrunePlan struct {
	Now     time.Time
	Before  time.Time // changes, batches, problems and dormant registrations older than this
	Dormant func(Registration) bool
}

// PruneCounts are what a prune removed, or would remove.
type PruneCounts struct {
	Changes       int `json:"changes"`
	Batches       int `json:"batches"`
	Problems      int `json:"problems"`
	Registrations int `json:"registrations"`
	Watches       int `json:"watches"`
}
