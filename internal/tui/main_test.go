package tui

import (
	"fmt"
	"os"
	"testing"

	"github.com/cajbecu/mcpick/internal/testguard"
)

// testRoot is where the sample catalog's files live: a throwaway directory,
// so that a test which reaches a write (v, d, +) edits nothing real.
var testRoot string

// TestMain gives the sample catalog throwaway paths, and fails the run if a
// test still managed to change the real ~/.claude.json: a picker over a
// hand-built catalog once pointed at the real file, and a pasted text that
// happened to spell v then g moved a server into it.
func TestMain(m *testing.M) {
	var err error
	if testRoot, err = os.MkdirTemp("", "mt"); err != nil {
		panic(err)
	}
	check := testguard.ClaudeJSON()
	code := m.Run()
	os.RemoveAll(testRoot)
	if err := check(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		code = 1
	}
	os.Exit(code)
}
