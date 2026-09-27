package backend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `gemini mcp list` is how a user — and the e2e run — checks what gemini
// will load. yargs rejects flags it does not know on a subcommand, so a
// session flag put in front of `mcp` made the listing fail with "Unknown
// arguments" instead of running. The project file must still be rewritten:
// it is what the listing reads.
func TestGeminiSubcommandGetsNoSessionFlags(t *testing.T) {
	ctx := testCtx(t)
	b := byName["gemini"]
	plan, err := b.Plan(ctx, remoteSel(), []string{"gemini", "mcp", "list"})
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Cleanup()
	if line := strings.Join(plan.Argv, " "); strings.Contains(line, "--allowed-mcp-server-names") {
		t.Errorf("session flag injected in front of a subcommand: %s", line)
	}
	if _, err := os.Stat(filepath.Join(ctx.Root, ".gemini", "settings.json")); err != nil {
		t.Errorf("the settings file the listing reads was not written: %v", err)
	}
	// A session, with or without a prompt, still gets its allow list.
	for _, argv := range [][]string{{"gemini"}, {"gemini", "-p", "hi"}, {"gemini", "--yolo"}} {
		if pv := b.Preview(remoteSel(), argv); !strings.Contains(strings.Join(pv.Argv, " "), "--allowed-mcp-server-names") {
			t.Errorf("%v lost its allow list: %v", argv, pv.Argv)
		}
	}
}
