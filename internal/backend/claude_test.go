package backend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cajbecu/mcpick/internal/catalog"
	"github.com/cajbecu/mcpick/internal/spec"
)

// [~]: a server disabled in Claude Code runs for the session under a name
// Claude's disabled list does not contain; a plugin server keeps its name,
// because Claude disables it under the namespaced one.
func TestSessionAliasesForDisabledServers(t *testing.T) {
	cat := &catalog.Catalog{Servers: []catalog.Server{
		{Name: "design", Disabled: true, DisabledAs: "design", Spec: map[string]any{"url": "https://d"}},
		{Name: "design_mcpick", Spec: map[string]any{"url": "https://taken"}},
		{Name: "cf-api", Origin: catalog.OriginPlugin, Disabled: true, DisabledAs: "plugin:cloudflare:cf-api", Spec: map[string]any{"url": "https://c"}},
	}}
	sel := spec.Selection{Names: []string{"design", "cf-api", "github"}, Specs: map[string]map[string]any{
		"design": {"url": "https://d"}, "cf-api": {"url": "https://c"}, "github": {"url": "https://a"},
	}}
	notes, err := claudeBackend{}.Prepare(Prep{Catalog: cat}, &sel)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(sel.Names, ",") != "design_mcpick2,cf-api,github" {
		t.Errorf("names = %v; the alias must avoid a name already in the catalog", sel.Names)
	}
	if sel.Specs["design_mcpick2"]["url"] != "https://d" || sel.Specs["design"] != nil {
		t.Errorf("specs = %v", sel.Specs)
	}
	if len(notes) != 2 {
		t.Errorf("notes = %q, want one per disabled server", notes)
	}
}

// [x]: the entry is removed from Claude Code's disabled list for good, and the
// server is then launched under its own name.
func TestPrepareReenablesCheckedServers(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	if err := os.WriteFile(path, []byte(`{"projects":{"/w":{"disabledMcpServers":["design","other"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cat := &catalog.Catalog{ClaudePath: path, ProjectKey: "/w", Servers: []catalog.Server{
		{Name: "design", Disabled: true, DisabledAs: "design"},
	}}
	sel := spec.Selection{Names: []string{"design"}, Specs: map[string]map[string]any{"design": {}}}
	if _, err := (claudeBackend{}).Prepare(Prep{Catalog: cat, Reenable: map[string]bool{"design": true}}, &sel); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	if strings.Contains(string(body), `"design"`) || !strings.Contains(string(body), `"other"`) {
		t.Errorf(".claude.json = %s", body)
	}
	if sel.Names[0] != "design" {
		t.Error("a re-enabled server needs no alias")
	}
}

// Without a mark in the picker, Claude's settings stay as they are: --select,
// --profile and -y must never change them.
func TestPrepareLeavesClaudeSettingsAloneUnasked(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	orig := []byte(`{"projects":{"/w":{"disabledMcpServers":["design"]}}}`)
	if err := os.WriteFile(path, orig, 0o600); err != nil {
		t.Fatal(err)
	}
	cat := &catalog.Catalog{ClaudePath: path, ProjectKey: "/w", Servers: []catalog.Server{
		{Name: "design", Disabled: true, DisabledAs: "design"},
	}}
	sel := spec.Selection{Names: []string{"design"}, Specs: map[string]map[string]any{"design": {}}}
	if _, err := (claudeBackend{}).Prepare(Prep{Catalog: cat}, &sel); err != nil {
		t.Fatal(err)
	}
	if body, _ := os.ReadFile(path); string(body) != string(orig) {
		t.Errorf(".claude.json changed: %s", body)
	}
}
