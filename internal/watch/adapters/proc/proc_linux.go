package proc

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
)

// startOf reads field 22 of /proc/<pid>/stat: the start time in clock ticks
// since boot. The command name, field 2, may hold spaces and parentheses,
// so fields are counted from its closing parenthesis.
func startOf(pid int) (string, bool, error) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	s := string(b)
	end := strings.LastIndexByte(s, ')')
	if end < 0 {
		return "", false, fmt.Errorf("/proc/%d/stat: no command name", pid)
	}
	fields := strings.Fields(s[end+1:])
	// fields[0] is field 3 (state), so field 22 is fields[19].
	if len(fields) < 20 {
		return "", false, fmt.Errorf("/proc/%d/stat: %d fields", pid, len(fields)+2)
	}
	if fields[0] == "Z" || fields[0] == "X" {
		return "", false, nil // a zombie has ended; only its exit status is left
	}
	return fields[19], true, nil
}
