package catalog

import (
	"fmt"
	"os"
	"testing"

	"github.com/cajbecu/mcpick/internal/testguard"
)

// TestMain fails the run if a test changed the real ~/.claude.json: this
// package is the one that writes it.
func TestMain(m *testing.M) {
	check := testguard.ClaudeJSON()
	code := m.Run()
	if err := check(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		code = 1
	}
	os.Exit(code)
}
