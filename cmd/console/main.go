// Command console is one of the three schmerz-reformen services.
//
// This file wires and nothing else: the service itself lives in
// internal/console. See CLAUDE.md, "Project layout — cmd/ is wiring only".
package main

import (
	"os"

	"github.com/ylallemant/schmerz-reformen/internal/cli"
	"github.com/ylallemant/schmerz-reformen/internal/console"
)

// Set at build time:
//
//	-ldflags "-X main.version=x.y.z -X main.commit=abc1234"
var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	os.Exit(cli.Execute(console.Definition(), version, commit))
}
