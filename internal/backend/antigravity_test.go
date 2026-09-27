package backend

import (
	"os"
	"strings"
	"testing"
)

// The Antigravity CLI does not read the file 0.1.0 rewrote (the end-to-end
// run found it listing "No MCP servers configured" whatever the selection),
// so the CLI is not supported: a launch writes nothing into the project,
// runs the command as given, and says so — in the picker's command line,
// at launch and in `mcpick agents` — rather than let the user rely on a
// selection that does not apply.
func TestAntigravityIsLaunchedUnchangedAndSaysItIsNotSupported(t *testing.T) {
	b := byName["antigravity"]
	if in := b.Info(); !strings.Contains(in.Summary, "not supported") || len(in.AlsoReads) != 0 {
		t.Errorf("info = %+v; agents must say the CLI is not supported, and there is nothing to report as leaking", in)
	}
	if pv := b.Preview(remoteSel(), []string{"agy", "mcp", "list"}); !strings.Contains(pv.Note, "not supported") ||
		strings.Join(pv.Argv, " ") != "agy mcp list" {
		t.Errorf("preview = %+v; the command must be shown unchanged, with the caveat", pv)
	}
	ctx := testCtx(t)
	plan, err := b.Plan(ctx, remoteSel(), []string{"agy", "mcp", "list"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Cleanup != nil || len(plan.Env) != 0 || strings.Join(plan.Argv, " ") != "agy mcp list" {
		t.Errorf("plan = %+v; agy must run exactly as given", plan)
	}
	if len(plan.Notes) != 1 || !strings.Contains(plan.Notes[0], "not supported") || !strings.Contains(plan.Notes[0], "~/.gemini/config/mcp_config.json") {
		t.Errorf("notes = %q; one line has to say the CLI is not supported and why", plan.Notes)
	}
	if entries, _ := os.ReadDir(ctx.Root); len(entries) != 0 {
		t.Errorf("a launch wrote into the project: %v", entries)
	}
	// The IDE's workspace file keeps its dialect, for export.
	data, err := b.Dialect().Emit(remoteSel())
	if err != nil || !strings.Contains(string(data), `"serverUrl"`) {
		t.Errorf("export dialect = %s, %v; the IDE's shape must stay", data, err)
	}
	// Other project-file agents carry no such caveat.
	if pv := byName["gemini"].Preview(remoteSel(), []string{"gemini"}); strings.Contains(pv.Note, "not supported") {
		t.Errorf("gemini's note carries antigravity's caveat: %q", pv.Note)
	}
}
