// Package home finds wakewire's folder, ~/.wakewire or $WAKEWIRE_HOME, and
// the files in it; see docs/reference/configuration.md#paths.
package home

import (
	"errors"
	"os"
	"path/filepath"
)

// Paths are the files wakewire keeps.
type Paths struct {
	Home        string `json:"home"`
	Config      string `json:"config"`
	Credentials string `json:"credentials"`
	State       string `json:"state"`
	Cache       string `json:"cache"`
}

// Resolve reads WAKEWIRE_HOME, else HOME, through getenv.
func Resolve(getenv func(string) string) (Paths, error) {
	dir := getenv("WAKEWIRE_HOME")
	if dir == "" {
		h := getenv("HOME")
		if h == "" {
			return Paths{}, errors.New("neither WAKEWIRE_HOME nor HOME is set")
		}
		dir = filepath.Join(h, ".wakewire")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Paths{}, err
	}
	return Paths{
		Home:        abs,
		Config:      filepath.Join(abs, "config.toml"),
		Credentials: filepath.Join(abs, "credentials.toml"),
		State:       filepath.Join(abs, "state.db"),
		Cache:       filepath.Join(abs, "cache"),
	}, nil
}

// Ensure creates the folder and cache/ with mode 700.
func (p Paths) Ensure() error {
	if err := os.MkdirAll(p.Home, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(p.Home, 0o700); err != nil { //nolint:gosec // a folder needs its search bit
		return err
	}
	return os.MkdirAll(p.Cache, 0o700)
}
