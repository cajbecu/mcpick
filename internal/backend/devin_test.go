package backend

import (
	"os"
	"path/filepath"
	"testing"
)

// Devin CLI 3 keeps MCP servers in .devin/mcp_config.json and, on start-up,
// migrates a mcpServers block it finds in .devin/config.json into it. Written
// to the old file, the selection was copied by devin into a file the restore
// knew nothing about — a project file changed for good, which the mechanism
// promises never to do.
func TestDevinWritesTheFileDevinReads(t *testing.T) {
	ctx := testCtx(t)
	plan, err := byName["devin"].Plan(ctx, remoteSel(), []string{"devin", "mcp", "list"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ctx.Root, ".devin", "mcp_config.json")); err != nil {
		t.Errorf(".devin/mcp_config.json not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(ctx.Root, ".devin", "config.json")); !os.IsNotExist(err) {
		t.Error(".devin/config.json was written; devin migrates servers out of it into mcp_config.json")
	}
	plan.Cleanup()
	if _, err := os.Stat(filepath.Join(ctx.Root, ".devin")); !os.IsNotExist(err) {
		t.Error(".devin left behind after the run")
	}
}
