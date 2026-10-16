// Package proc reads the process table: whether a process id runs, and
// its start time, which tells it apart from a later program under the same
// id; see state.md, "Liveness".
package proc

// Table is the operating system's process table.
type Table struct{}

// StartOf answers pid's start time, and false when no such process runs.
func (Table) StartOf(pid int) (string, bool, error) {
	if pid <= 0 {
		return "", false, nil
	}
	return startOf(pid)
}
