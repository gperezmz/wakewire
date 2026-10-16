//go:build !linux && !darwin

package proc

import "errors"

func startOf(int) (string, bool, error) {
	return "", false, errors.New("wakewire reads process start times on Linux and macOS only")
}
