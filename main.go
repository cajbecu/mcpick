// Command mcpick chooses which MCP servers a coding-agent session loads, at the
// moment the session is launched.
//
// The work happens in internal/; this file only hands over the arguments and
// the version the release build linked in.
package main

import (
	"os"

	"github.com/cajbecu/mcpick/internal/cli"
)

// version is set by release builds: -ldflags "-X main.version=1.2.3". Builds
// without it report the module version, or "dev".
var version = "dev"

func main() {
	os.Exit(cli.Main(os.Args[1:], version))
}
