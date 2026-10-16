package proc

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// startOf reads kp_proc.p_starttime from sysctl kern.proc.pid.<pid>.
// For a process that does not exist, sysctl answers no record, which
// golang.org/x/sys reports as EIO.
func startOf(pid int) (string, bool, error) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if errors.Is(err, unix.ESRCH) || errors.Is(err, unix.EIO) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if kp.Proc.P_pid != int32(pid) || kp.Proc.P_stat == 5 { // SZOMB
		return "", false, nil
	}
	t := kp.Proc.P_starttime
	return fmt.Sprintf("%d.%06d", t.Sec, t.Usec), true, nil
}
