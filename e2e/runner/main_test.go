package main

import (
	"errors"
	"strings"
	"testing"
)

// The verdict for level (a) hangs on reading the picker's frame. A misread
// box would either pass a run that picked the wrong servers or fail a
// correct one; the row format is the picker's, with the cursor and colours
// stripped by tmux.
func TestCheckedReadsPickerRows(t *testing.T) {
	frame := strings.Join([]string{
		"mcpick e2e uid=e2e-claude · 3/5 selected  ✻ claude",
		"→ claude --mcp-config <rendered config> --strict-mcp-config --version",
		"",
		"Project • 3/5 selected  /e2e/project/.mcp.yaml",
		"> [x] srv-alpha            fakemcp --name srv-alpha --log /e2e/logs/srv-alpha.log",
		"  [ ] srv-bravo            http://127.0.0.1:18081/mcp",
		"  [x] srv-charlie          fakemcp --name srv-charlie --log /e2e/logs/srv-charlie.log",
		"  [x] srv-delta            http://127.0.0.1:18082/mcp",
		"  [ ] srv-echo             fakemcp --name srv-echo --log /e2e/logs/srv-echo.log",
	}, "\n")
	got := checked(frame)
	if len(got) != 5 {
		t.Fatalf("read %d rows, want 5: %v", len(got), got)
	}
	if !sameSet(keysWhere(got, true), chosen) {
		t.Errorf("checked = %v, want %v", keysWhere(got, true), chosen)
	}
	if !sameSet(keysWhere(got, false), unchosen()) {
		t.Errorf("unchecked = %v, want %v", keysWhere(got, false), unchosen())
	}
}

// Level (b) by listing must reject an agent that lists an extra server as
// firmly as one that misses a chosen one: "loads exactly the chosen" is the
// property, and a leak is the bug mcpick exists to prevent.
func TestListingJudgesExactness(t *testing.T) {
	exact := "Name   Command\nsrv-alpha  fakemcp\nsrv-charlie fakemcp\n\nName Url\nsrv-delta http://127.0.0.1:18082/mcp\n"
	if !sameSet(names2quoted(exact), chosen) {
		t.Errorf("exact listing read as %v", names2quoted(exact))
	}
	leak := exact + "srv-echo fakemcp\n"
	if sameSet(names2quoted(leak), chosen) {
		t.Error("a listing with an unchosen server passed")
	}
	short := strings.Replace(exact, "srv-delta http://127.0.0.1:18082/mcp\n", "", 1)
	if sameSet(names2quoted(short), chosen) {
		t.Error("a listing missing a chosen server passed")
	}
	// Names are matched as words: a name that merely prefixes another must
	// not count for it.
	if got := names2quoted("srv-alphabet\n"); len(got) != 0 {
		t.Errorf("prefix matched as a name: %v", got)
	}
	// mcpick's own debug line names the allow list; it is not the agent's
	// listing and must not make an empty listing look complete.
	debug := "mcpick: exec: MCPICK_CONFIG=/x gemini --allowed-mcp-server-names srv-alpha,srv-charlie,srv-delta mcp list\n"
	if got := names2quoted(withoutMcpick(debug)); len(got) != 0 {
		t.Errorf("mcpick's own output counted as a listing: %v", got)
	}
}

// The end of a launch is read from the pane, not from tmux's pane_dead:
// that flag waits for every holder of the tty, and a CLI's background
// update check can hold it for minutes after --version returned.
func TestExitStatusFromPane(t *testing.T) {
	pane := "codex-cli 0.157.0\n" + exitMark + "=0\n"
	if got := exitStatus(pane); got != 0 {
		t.Errorf("status = %d, want 0", got)
	}
	if got := exitStatus("Not logged in\n" + exitMark + "=130\n"); got != 130 {
		t.Errorf("status = %d, want 130", got)
	}
	if got := exitStatus("still running"); got != -1 {
		t.Errorf("status without a mark = %d, want -1", got)
	}
}

// The fakes' log format is the contract with fakemcp; the runner has to
// read a server and client out of it, and ignore every other event, or a
// tools/list would count as a second connection.
func TestParseInitialize(t *testing.T) {
	server, client, ok := parseInitialize("2026-09-25T11:42:04.628848660Z srv-alpha stdio initialize client=claude-code/2.1.282 protocol=2025-11-25")
	if !ok || server != "srv-alpha" || client != "claude-code/2.1.282" {
		t.Errorf("got %q %q %v", server, client, ok)
	}
	for _, line := range []string{
		"2026-09-25T11:42:04.629007077Z srv-alpha stdio tools/list ",
		"2026-09-25T11:42:04.628568242Z srv-alpha stdio start pid=684",
		"",
	} {
		if _, _, ok := parseInitialize(line); ok {
			t.Errorf("%q read as an initialize", line)
		}
	}
}

// The catalog the runner writes is what the picker shows; every fake must be
// in it under its own name, HTTP ones by URL and stdio ones by command, or
// level (a) would be testing a different list than level (b) launches.
func TestCatalogNamesEveryFake(t *testing.T) {
	cat := catalog()
	for _, f := range fakes {
		if !strings.Contains(cat, "  "+f.name+":\n") {
			t.Errorf("catalog lacks %s:\n%s", f.name, cat)
		}
		if f.addr != "" && !strings.Contains(cat, "http://"+f.addr+"/mcp") {
			t.Errorf("catalog lacks the URL of %s", f.name)
		}
	}
	if strings.Count(cat, "command: fakemcp") != 3 || strings.Count(cat, "type: http") != 2 {
		t.Errorf("want 3 stdio and 2 http entries:\n%s", cat)
	}
}

// A known gap excuses one thing: the documented mismatch, from a probe that
// otherwise ran to completion. A gap agent that times out, leaves its
// directory behind, exits non-zero or fails some other way has found a new
// bug, and the run must say so instead of filing it under the gap.
func TestGapExcusesOnlyTheDocumentedFailure(t *testing.T) {
	agy := agent{name: "antigravity", gap: "reads the global file only", gapSays: "No MCP servers configured"}
	mismatch := []string{"listing names [], want exactly [srv-alpha srv-charlie srv-delta]"}
	expected := findings{mismatches: mismatch, probe: "No MCP servers configured.\n"}

	if v, d := judge(agy, expected); v != "gap" || !strings.Contains(d, agy.gap) {
		t.Errorf("the documented failure judged %q: %s", v, d)
	}
	cases := map[string]findings{
		"timeout":   {mismatches: mismatch, probe: expected.probe, broken: []string{"the probe ran past 3m0s"}, runErr: errors.New("context deadline exceeded")},
		"leftover":  {mismatches: mismatch, probe: expected.probe, broken: []string{".agents was left in the project after the run"}},
		"bad log":   {mismatches: mismatch, probe: expected.probe, broken: []string{"srv-alpha logged a line for srv-echo"}},
		"exit 1":    {mismatches: mismatch, probe: expected.probe, runErr: errors.New("exit status 1")},
		"says else": {mismatches: []string{"listing names [srv-echo], want exactly [srv-alpha srv-charlie srv-delta]"}, probe: "srv-echo fakemcp\n"},
		"passes":    {evidence: []string{"listing names exactly the three"}, probe: "srv-alpha\nsrv-charlie\nsrv-delta\n"},
	}
	for name, f := range cases {
		if v, d := judge(agy, f); v != "FAIL" {
			t.Errorf("%s judged %q, want FAIL: %s", name, v, d)
		}
	}
	// A gap without a documented output can never be matched: an agent
	// note that forgot it fails until it says what it looks like.
	vague := agent{name: "x", gap: "something"}
	if v, _ := judge(vague, expected); v != "FAIL" {
		t.Errorf("a gap with no gapSays judged %q, want FAIL", v)
	}
	// Agents without a gap are unchanged: a mismatch or a broken invariant
	// fails, a clean run passes with its evidence.
	plain := agent{name: "codex"}
	if v, d := judge(plain, findings{evidence: []string{"ok"}}); v != "pass" || d != "ok" {
		t.Errorf("clean run judged %q %q", v, d)
	}
	if v, _ := judge(plain, findings{mismatches: mismatch}); v != "FAIL" {
		t.Errorf("mismatch judged %q, want FAIL", v)
	}
	if v, _ := judge(plain, findings{broken: []string{".grok was left in the project after the run"}}); v != "FAIL" {
		t.Errorf("leftover judged %q, want FAIL", v)
	}
}

// An agent mcpick does not support is judged on the launch, not on the
// listing: a probe that exits 0 with mcpick's notice on stderr and nothing
// left in the project is "not supported", never a FAIL and never a pass;
// a probe that fails, a missing notice or a leftover directory is a FAIL,
// since mcpick promised to launch the agent unchanged and say so.
func TestUnsupportedAgentIsJudgedOnTheLaunch(t *testing.T) {
	agy := agent{name: "antigravity", cmd: "agy", unsupported: "reads the global file only"}
	if v, d := judgeUnsupported(agy, findings{notice: true}); v != "not supported" || !strings.Contains(d, agy.unsupported) {
		t.Errorf("a sound launch judged %q: %s", v, d)
	}
	for name, f := range map[string]findings{
		"no notice": {},
		"exit 1":    {notice: true, runErr: errors.New("exit status 1")},
		"leftover":  {notice: true, broken: []string{".agents was left in the project after the run"}},
	} {
		if v, _ := judgeUnsupported(agy, f); v != "FAIL" {
			t.Errorf("%s judged %q, want FAIL", name, v)
		}
	}
	for _, a := range agents {
		if a.unsupported != "" && (a.gap != "" || a.proof != 0) {
			t.Errorf("%s is unsupported and also has a gap or a proof", a.name)
		}
	}
}

// Every gap in the registry must say what its probe prints, or judge
// could never accept it and the note would be a permanent FAIL.
func TestEveryGapSaysWhatItPrints(t *testing.T) {
	for _, a := range agents {
		if a.gap != "" && a.gapSays == "" {
			t.Errorf("%s has a gap note without gapSays", a.name)
		}
		if a.gap == "" && a.gapSays != "" {
			t.Errorf("%s has gapSays without a gap note", a.name)
		}
	}
}

// -only with a typo used to select nothing and report a passing empty run.
// Every name must be an agent, and an explicit filter must name at least
// one; the executable's name (agy) is accepted alongside mcpick's.
func TestOnlyRejectsUnknownAndEmpty(t *testing.T) {
	all, err := selectAgents("", agents)
	if err != nil || len(all) != len(agents) {
		t.Fatalf("no filter: %d agents, %v", len(all), err)
	}
	some, err := selectAgents(" codex, agy ,gemini,codex", agents)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, a := range some {
		names = append(names, a.name)
	}
	if strings.Join(names, ",") != "codex,gemini,antigravity" {
		t.Errorf("selected %v: want codex, gemini, antigravity in registry order, once each", names)
	}
	for _, only := range []string{"nonexistent-agent", "codex,nope,gemini", ",", " "} {
		got, err := selectAgents(only, agents)
		if err == nil {
			t.Errorf("-only %q selected %d agent(s) without an error", only, len(got))
			continue
		}
		if only == "codex,nope,gemini" && (!strings.Contains(err.Error(), "nope") || strings.Contains(err.Error(), "unknown agent(s) codex")) {
			t.Errorf("error names the wrong agents: %v", err)
		}
	}
}
