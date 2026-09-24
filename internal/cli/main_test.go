package cli

import (
	"fmt"
	"os"
	"testing"
)

// TestMain points every test at a throwaway home, and fails the run if a test
// still managed to write into the real one: tests must never touch the
// selections, tokens or runtime files of whoever runs them.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "mcpick-test-home-")
	if err != nil {
		panic(err)
	}
	real, _ := os.UserHomeDir()
	os.Setenv("MCPICK_HOME", home)
	_, existedBefore := os.Stat(real + "/.mcpick")
	code := m.Run()
	os.RemoveAll(home)
	if _, err := os.Stat(real + "/.mcpick"); os.IsNotExist(existedBefore) && err == nil {
		fmt.Fprintln(os.Stderr, "a test wrote into the real ~/.mcpick")
		code = 1
	}
	os.Exit(code)
}
