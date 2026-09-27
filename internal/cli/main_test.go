package cli

import (
	"fmt"
	"os"
	"testing"

	"github.com/cajbecu/mcpick/internal/testguard"
)

// TestMain points every test at a throwaway home, and fails the run if a test
// still managed to write into the real one: tests must never touch the
// selections, tokens or runtime files of whoever runs them.
func TestMain(m *testing.M) {
	// A test catalog can name this binary as a stdio server. Started that
	// way it writes the file MCPICK_TEST_TOUCH names and exits, which proves
	// that a command ran and that its env reached it — with no shell
	// involved, because Windows CI has none.
	if marker := os.Getenv("MCPICK_TEST_TOUCH"); marker != "" {
		if err := os.WriteFile(marker, []byte("ran\n"), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	home, err := os.MkdirTemp("", "mcpick-test-home-")
	if err != nil {
		panic(err)
	}
	real, _ := os.UserHomeDir()
	os.Setenv("MCPICK_HOME", home)
	_, existedBefore := os.Stat(real + "/.mcpick")
	check := testguard.ClaudeJSON()
	code := m.Run()
	if err := check(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		code = 1
	}
	os.RemoveAll(home)
	if _, err := os.Stat(real + "/.mcpick"); os.IsNotExist(existedBefore) && err == nil {
		fmt.Fprintln(os.Stderr, "a test wrote into the real ~/.mcpick")
		code = 1
	}
	os.Exit(code)
}
