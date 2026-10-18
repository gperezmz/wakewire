// Package wiring is the composition root: it names every concrete adapter
// and hands the transports what it built.
package wiring

import (
	"runtime/debug"

	"github.com/gperezmz/wakewire/v2/internal/watch/domain"
)

// Build is what `wakewire version` reports.
type Build struct {
	Version  string `json:"version"`
	Commit   string `json:"commit"`
	Protocol int    `json:"protocol"`
}

// ReadBuild takes the release build's stamped values and falls back to what
// the Go toolchain recorded, which is all a `go install` build carries.
func ReadBuild(version, commit string) Build {
	b := Build{Version: version, Commit: commit, Protocol: domain.Protocol}
	if info, ok := debug.ReadBuildInfo(); ok {
		if b.Version == "" && info.Main.Version != "" && info.Main.Version != "(devel)" {
			b.Version = info.Main.Version
		}
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" && b.Commit == "" {
				b.Commit = s.Value
			}
		}
	}
	if b.Version == "" {
		b.Version = "dev"
	}
	if b.Commit == "" {
		b.Commit = "unknown"
	}
	return b
}
