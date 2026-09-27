package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// `mcpick import` writes ${VAR:?export VAR} references for the credentials
// it finds and names the variables; --yes copies the values as they are and
// warns; --redact, the old spelling of the default, still works.
func TestImportRedactsByDefault(t *testing.T) {
	root, claudePath, run := moveWorkspace(t)
	yaml := filepath.Join(root, ".mcp.yaml")

	out, errw, err := run("import")
	if err != nil {
		t.Fatal(err)
	}
	y, _ := os.ReadFile(yaml)
	if strings.Contains(string(y), "live-token") {
		t.Fatalf("import copied a credential verbatim:\n%s", y)
	}
	if !strings.Contains(string(y), "Bearer ${SECRET_AUTHORIZATION:?export SECRET_AUTHORIZATION}") {
		t.Errorf("import should write a reference that fails loudly when unset:\n%s", y)
	}
	if !strings.Contains(out, "export SECRET_AUTHORIZATION=") {
		t.Errorf("stdout = %q; the variable to export must be named", out)
	}
	if !strings.Contains(errw, "still in ~/.claude.json") {
		t.Errorf("stderr = %q; where the value still is must be said", errw)
	}
	// The imported servers are now in both files; the way to one
	// definition is said, whichever tool the user prefers.
	if !strings.Contains(out, "which the catalog now shadows") || !strings.Contains(out, "claude mcp remove <name>") || !strings.Contains(out, "mcpick move <name> project") {
		t.Errorf("stdout = %q; import should say how to remove the copies from ~/.claude.json", out)
	}
	if data, _ := os.ReadFile(claudePath); !strings.Contains(string(data), "live-token") {
		t.Error("import must not touch ~/.claude.json")
	}

	root, _, run = moveWorkspace(t)
	if _, _, err := run("import", "--redact"); err != nil {
		t.Fatal(err)
	}
	if y, _ := os.ReadFile(filepath.Join(root, ".mcp.yaml")); strings.Contains(string(y), "live-token") {
		t.Errorf("--redact should still redact:\n%s", y)
	}

	root, _, run = moveWorkspace(t)
	_, errw, err = run("--yes", "import")
	if err != nil {
		t.Fatal(err)
	}
	if y, _ := os.ReadFile(filepath.Join(root, ".mcp.yaml")); !strings.Contains(string(y), "Bearer live-token") {
		t.Errorf("--yes should copy the value:\n%s", y)
	}
	if !strings.Contains(errw, "copied as they are") || !strings.Contains(errw, "SECRET_AUTHORIZATION") {
		t.Errorf("stderr = %q; --yes must warn and name what was copied", errw)
	}
}
