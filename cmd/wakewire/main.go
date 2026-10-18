// Command wakewire wakes an agent session when what it waits on changes on
// GitHub, GitLab or Jira Cloud. It runs, answers and exits; see docs/.
package main

import (
	"context"
	"os"

	"github.com/gperezmz/wakewire/v2/internal/cli"
	"github.com/gperezmz/wakewire/v2/internal/wiring"
)

// Set by the release build through -ldflags.
var (
	version = ""
	commit  = ""
)

func main() {
	build := wiring.ReadBuild(version, commit)
	os.Exit(cli.Run(context.Background(), os.Args, build, os.Stdin, os.Stdout, os.Stderr))
}
