package domain

import "time"

// HolderFacts are what liveness is worked out from each time someone asks,
// never a flag someone has to clear; see state.md, "Liveness".
type HolderFacts struct {
	ProcessRunning bool // the process id runs, with the recorded start time
	Ended          bool // `adapter ended` was recorded for the session
	Cleared        bool // the same process reported a newer session id
	HeardAt        time.Time
	Interval       time.Duration
	Now            time.Time
}

// Liveness answers whether a registration holds its watch, and if not, why
// it is dormant.
func Liveness(f HolderFacts) (holds bool, why string) {
	switch {
	case f.Ended: // the adapter said so: the truest reason, so it comes first
		return false, "its session ended"
	case !f.ProcessRunning:
		return false, "its process is gone"
	case f.Cleared:
		return false, "its session was cleared"
	case f.Now.Sub(f.HeardAt) > time.Duration(DormantAfter)*f.Interval:
		return false, "its adapter has not been heard from for 3 intervals"
	}
	return true, ""
}
