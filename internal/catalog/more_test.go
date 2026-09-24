package catalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --strict-mcp-config drops plugin servers along with everything else, so a
// user with a plugin-provided server lost it the moment they used mcpick.
func TestPluginServersAreDiscovered(t *testing.T) {
	root := t.TempDir()
	claude := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claude)

	enabled := filepath.Join(claude, "plugins", "cache", "acme", "1.0")
	disabled := filepath.Join(claude, "plugins", "cache", "off", "1.0")
	write(t, filepath.Join(enabled, ".mcp.json"), `{"mcpServers":{
		"acme-api": {"type":"http","url":"https://acme/mcp"},
		"acme-local": {"command":"${CLAUDE_PLUGIN_ROOT}/bin/server","args":["--root","${CLAUDE_PLUGIN_ROOT}"]}
	}}`)
	write(t, filepath.Join(disabled, ".claude-plugin", "plugin.json"), `{"mcpServers":{"off-api":{"url":"https://off"}}}`)
	write(t, filepath.Join(claude, "plugins", "installed_plugins.json"), `{"version":2,"plugins":{
		"acme@m": [{"installPath": `+jsonStr(enabled)+`}],
		"off@m":  [{"installPath": `+jsonStr(disabled)+`}]
	}}`)
	write(t, filepath.Join(claude, "settings.json"), `{"enabledPlugins":{"acme@m":true,"off@m":false}}`)

	cat, err := Load(filepath.Join(root, ".mcp.yaml"), root, root)
	if err != nil {
		t.Fatal(err)
	}
	api, ok := cat.Find("acme-api")
	if !ok || api.Origin != OriginPlugin {
		t.Fatalf("acme-api = %+v, %v", api, ok)
	}
	local, _ := cat.Find("acme-local")
	if got := local.Endpoint(); got != enabled+"/bin/server --root "+enabled {
		t.Errorf("${CLAUDE_PLUGIN_ROOT} not expanded: %s", got)
	}
	if _, ok := cat.Find("off-api"); ok {
		t.Error("a disabled plugin's servers must not appear")
	}
}

// A stray ~/.mcp.yaml used to become the root of every repository under the
// home directory, because catalog files were searched all the way up before
// .git was considered.
func TestWorkspaceRootStopsAtNearestRepository(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, ".mcp.yaml"), "servers: {}\n")
	repo := filepath.Join(home, "src", "project")
	write(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/main\n")
	sub := filepath.Join(repo, "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := WorkspaceRoot(sub); got != repo {
		t.Errorf("root = %s, want the repository %s", got, repo)
	}
}

func TestDeleteProfile(t *testing.T) {
	for _, name := range []string{".mcp.yaml", ".mcp.json"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), name)
			if err := SaveProfile(path, "a", []string{"x"}); err != nil {
				t.Fatal(err)
			}
			if err := SaveProfile(path, "b", []string{"y"}); err != nil {
				t.Fatal(err)
			}
			if err := DeleteProfile(path, "a"); err != nil {
				t.Fatal(err)
			}
			_, _, profiles, err := ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := profiles["a"]; ok || len(profiles["b"]) != 1 {
				t.Errorf("profiles = %v", profiles)
			}
			if err := DeleteProfile(path, "a"); err == nil {
				t.Error("deleting a missing profile must be an error, so a typo does not look like success")
			}
		})
	}
}

func TestProfileWithUnknownServerWarns(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	write(t, filepath.Join(root, ".mcp.yaml"), "servers:\n  a: {url: https://x}\nprofiles:\n  p: [a, gone]\n")
	cat, err := Load(filepath.Join(root, ".mcp.yaml"), root, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Warnings) != 1 || !strings.Contains(cat.Warnings[0], "gone") {
		t.Errorf("warnings = %v, want one naming the missing server", cat.Warnings)
	}
}

// Editing a JSON catalog must not reorder it or escape characters the user
// wrote plainly: both show up as noise in their diff.
func TestJSONEditKeepsOrderAndText(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".mcp.json")
	write(t, path, `{
  "$schema": "x",
  "mcpServers": {
    "zeta": {"url": "https://x/z?a=1&b=2"},
    "alpha": {"url": "https://x/a"}
  }
}`)
	if err := AddServer(path, "mid", map[string]any{"url": "https://x/m?c=3&d=<4>"}); err != nil {
		t.Fatal(err)
	}
	names, _, _, err := ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "zeta,alpha,mid" {
		t.Errorf("order = %v", names)
	}
	body, _ := os.ReadFile(path)
	if strings.Contains(string(body), `\u0026`) || strings.Contains(string(body), `\u003c`) {
		t.Errorf("characters were HTML-escaped:\n%s", body)
	}
	if !strings.HasPrefix(string(body), "{\n  \"$schema\"") {
		t.Errorf("the file's own key order was lost:\n%s", body)
	}
}

func TestDuplicateAcrossCatalogFilesWarns(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	write(t, filepath.Join(root, ".mcp.yaml"), "servers:\n  a: {url: https://yaml}\n")
	write(t, filepath.Join(root, ".mcp.json"), `{"mcpServers":{"a":{"url":"https://json"}}}`)
	cat, err := Load(filepath.Join(root, ".mcp.yaml"), root, root)
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := cat.Find("a"); s.Endpoint() != "https://yaml" {
		t.Errorf("a = %s, want the yaml entry", s.Endpoint())
	}
	if len(cat.Warnings) != 1 {
		t.Errorf("warnings = %v, want the shadowed definition reported", cat.Warnings)
	}
}

func TestMalformedServerEntryIsAnError(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".mcp.yaml"), "servers:\n  a: [not, a, map]\n")
	if _, _, _, err := ReadFile(filepath.Join(root, ".mcp.yaml")); err == nil {
		t.Fatal("a server that is not a mapping used to vanish silently")
	}
}

func TestBackupsArePruned(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude.json")
	for i := 0; i < 8; i++ {
		write(t, path+".mcpick-bak-2026010"+string(rune('0'+i))+"T000000Z", "{}")
	}
	pruneBackups(path, 5)
	matches, _ := filepath.Glob(path + ".mcpick-bak-*")
	if len(matches) != 5 {
		t.Errorf("%d backups left, want 5", len(matches))
	}
}

// ~/.claude.json holds conversation history full of <, > and &. Deleting one
// server must not rewrite all of it with \u escapes.
func TestDeleteLeavesOtherContentByteIdentical(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	write(t, path, `{
  "history": ["a < b && c > d"],
  "mcpServers": {
    "drop": {"url": "https://x"},
    "keep": {"url": "https://y"}
  }
}`)
	if err := DeleteFromClaudeJSON(path, "/w", "drop", OriginGlobal); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), `"a < b && c > d"`) {
		t.Errorf("unrelated content was re-encoded:\n%s", body)
	}
}

// Claude Code records a disabled plugin server as plugin:<plugin>:<server>.
// mcpick hands the server over under its plain name, which that entry does
// not match, so without this check selecting it would quietly re-enable
// something the user switched off.
func TestDisabledPluginServerIsFlagged(t *testing.T) {
	root := t.TempDir()
	claude := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	inst := filepath.Join(claude, "plugins", "cache", "cf", "1.0")
	write(t, filepath.Join(inst, ".mcp.json"), `{"mcpServers":{"cf-api":{"url":"https://a"},"cf-docs":{"url":"https://d"}}}`)
	write(t, filepath.Join(claude, "plugins", "installed_plugins.json"),
		`{"plugins":{"cloudflare@cloudflare":[{"installPath":`+jsonStr(inst)+`}]}}`)
	write(t, filepath.Join(claude, "settings.json"), `{"enabledPlugins":{"cloudflare@cloudflare":true}}`)
	write(t, filepath.Join(claude, ".claude.json"), `{"projects":{`+jsonStr(root)+`:{"disabledMcpServers":["plugin:cloudflare:cf-api"]}}}`)

	cat, err := Load(filepath.Join(root, ".mcp.yaml"), root, root)
	if err != nil {
		t.Fatal(err)
	}
	api, _ := cat.Find("cf-api")
	docs, _ := cat.Find("cf-docs")
	if !api.Disabled {
		t.Error("cf-api is disabled in Claude Code under its plugin name and must be flagged")
	}
	if docs.Disabled {
		t.Error("cf-docs is not disabled")
	}
}

// Re-enabling removes exactly the entries asked for, from the global list and
// this project's, and leaves every other disabled server and all other state.
func TestEnableInClaude(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".claude.json")
	write(t, path, `{
  "numStartups": 3,
  "disabledMcpServers": ["global-off", "stay-off"],
  "projects": {
    "/w": {"disabledMcpServers": ["claude_design", "plugin:cloudflare:cf-api", "keep"], "history": ["a < b"]},
    "/other": {"disabledMcpServers": ["claude_design"]}
  }
}`)
	if err := EnableInClaude(path, "/w", []string{"claude_design", "plugin:cloudflare:cf-api", "global-off"}); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	var got struct {
		Disabled []string `json:"disabledMcpServers"`
		Projects map[string]struct {
			Disabled []string `json:"disabledMcpServers"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.Disabled, ",") != "stay-off" {
		t.Errorf("global = %v, want [stay-off]", got.Disabled)
	}
	if strings.Join(got.Projects["/w"].Disabled, ",") != "keep" {
		t.Errorf("project = %v, want [keep]", got.Projects["/w"].Disabled)
	}
	if strings.Join(got.Projects["/other"].Disabled, ",") != "claude_design" {
		t.Error("another project's list must not be touched")
	}
	if !strings.Contains(string(body), `"a < b"`) {
		t.Error("unrelated content was re-encoded")
	}
}

func TestDisabledAsNamesTheEntry(t *testing.T) {
	root := t.TempDir()
	claude := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	write(t, filepath.Join(claude, ".claude.json"), `{"mcpServers":{"own":{"url":"https://o"}},"disabledMcpServers":["own"]}`)
	cat, err := Load(filepath.Join(root, ".mcp.yaml"), root, root)
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := cat.Find("own"); s.DisabledAs != "own" {
		t.Errorf("DisabledAs = %q", s.DisabledAs)
	}
}

func TestClaudeNames(t *testing.T) {
	root := t.TempDir()
	claude := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", claude)
	inst := filepath.Join(claude, "plugins", "cache", "cf", "1.0")
	write(t, filepath.Join(inst, ".mcp.json"), `{"mcpServers":{"cf-api":{"url":"https://a"}}}`)
	write(t, filepath.Join(claude, "plugins", "installed_plugins.json"),
		`{"plugins":{"cloudflare@cloudflare":[{"installPath":`+jsonStr(inst)+`}]}}`)
	write(t, filepath.Join(claude, "settings.json"), `{"enabledPlugins":{"cloudflare@cloudflare":true}}`)
	write(t, filepath.Join(claude, ".claude.json"), `{"mcpServers":{"own":{"url":"https://o"}}}`)
	cat, err := Load(filepath.Join(root, ".mcp.yaml"), root, root)
	if err != nil {
		t.Fatal(err)
	}
	names := cat.ClaudeNames()
	if names["own"] != "own" || names["cf-api"] != "plugin:cloudflare:cf-api" {
		t.Errorf("names = %v", names)
	}
}
