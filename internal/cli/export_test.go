package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// export prints the rendered config, credentials expanded: stderr says so
// once, and says when nothing is selected, since stdout is usually a file.
func TestExportWarnsAboutExpandedCredentialsAndEmptySelection(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MCPICK_HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	t.Setenv("MCPICK_TEST_TOKEN", "live-token-value")
	cat := "servers:\n" +
		"  gh: {type: http, url: https://example.com/mcp, headers: {Authorization: \"Bearer ${MCPICK_TEST_TOKEN}\"}}\n" +
		"  plain: {type: http, url: https://example.com/plain}\n"
	if err := os.WriteFile(filepath.Join(root, ".mcp.yaml"), []byte(cat), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (out, errw string) {
		t.Helper()
		var o, e strings.Builder
		a := &app{out: &o, errw: &e, version: "test"}
		if err := a.run(args); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		return o.String(), e.String()
	}
	out, errw := run("--select", "gh", "--agent", "codex", "export")
	if !strings.Contains(out, "live-token-value") {
		t.Fatalf("export should still render the credential:\n%s", out)
	}
	if !strings.Contains(errw, "export wrote expanded credentials to stdout; keep it out of files you commit") {
		t.Errorf("stderr = %q; the expanded credential must be warned about", errw)
	}
	if _, errw := run("--select", "plain", "--agent", "codex", "export"); strings.Contains(errw, "credentials") {
		t.Errorf("stderr = %q; a selection without credentials needs no warning", errw)
	}
	if _, errw := run("--none", "--agent", "codex", "export"); !strings.Contains(errw, "nothing is selected") {
		t.Errorf("stderr = %q; an empty export must be said", errw)
	}
	// --target, the 0.1.0 name, renders the same dialect.
	if out, _ := run("--select", "plain", "--target", "codex", "export"); !strings.Contains(out, "[mcp_servers.plain]") {
		t.Errorf("--target codex export = %q, want codex's TOML", out)
	}
}
