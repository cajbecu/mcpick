package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cajbecu/mcpick/internal/catalog"
	"github.com/cajbecu/mcpick/internal/proc"
	"github.com/cajbecu/mcpick/internal/trust"
)

// catalogWorkspace is trustWorkspace plus three repository remotes: envy,
// with a `${MCPICK_REVIEW_TOKEN}` header; lan, on a loopback address; and
// pub, a public URL with a literal Authorization header. None is reachable
// — the point is whether a measurement tries — so a remote that is gated
// says "skipped" and one that is not says how the connection failed.
func catalogWorkspace(t *testing.T) (marker string, root string, run func(args ...string) (out, errw string, err error)) {
	t.Helper()
	marker, run = trustWorkspaceWith(t, "  envy:\n    type: http\n    url: http://127.0.0.1:1/mcp\n"+
		"    headers: {Authorization: \"Bearer ${MCPICK_REVIEW_TOKEN}\", X-Plain: literal-value}\n"+
		"  lan:\n    type: http\n    url: http://127.0.0.1:1/mcp\n"+
		"  pub:\n    type: http\n    url: https://mcp.example.invalid/mcp\n    headers: {Authorization: \"Bearer literal-secret\"}\n")
	root, _ = os.Getwd()
	return marker, root, run
}

// A repository remote that would send a placeholder, or reach a private
// address, is skipped unselected, and the row says which; the harmless one
// is measured, and only onto a public address (a lookup failure here, since
// nothing resolves .invalid — but a try, not a skip).
func TestMeasureSkipsExposingRepositoryRemotes(t *testing.T) {
	_, _, run := catalogWorkspace(t)
	out, errw, err := run("measure")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"sends ${MCPICK_REVIEW_TOKEN} to its host", "private address", "--trust-catalog"} {
		if !strings.Contains(out+errw, want) {
			t.Errorf("output lacks %q:\n%s%s", want, out, errw)
		}
	}
	if strings.Contains(out+errw, "literal-value") || strings.Contains(out+errw, "literal-secret") {
		t.Errorf("a header value was printed:\n%s%s", out, errw)
	}
	jsonOut, _, _ := run("--json", "measure")
	if !strings.Contains(jsonOut, `"name": "pub"`) || strings.Contains(jsonOut, `"name": "pub",
    "ok": false,
    "error": "skipped`) {
		t.Errorf("pub should be tried, not skipped:\n%s", jsonOut)
	}
	for _, n := range []string{"envy", "lan"} {
		if !strings.Contains(jsonOut, `"name": "`+n+`"`) {
			t.Errorf("%s missing from --json", n)
		}
	}
	if !strings.Contains(jsonOut, `"skipped": true`) {
		t.Errorf("no row is marked skipped:\n%s", jsonOut)
	}
}

// The gate hands the public-only option to the probe for a harmless
// repository remote and to nothing else.
func TestGateMarksHarmlessRemotePublicOnly(t *testing.T) {
	_, root, _ := catalogWorkspace(t)
	var out, errw strings.Builder
	a := &app{out: &out, errw: &errw, version: "test", root: root, cwd: root}
	var err error
	if a.cat, err = catalog.Load(catalog.Path(root), root, root); err != nil {
		t.Fatal(err)
	}
	g := a.gate(a.cat.Servers, map[string]bool{})
	if !g.run["pub"] || !g.opts["pub"].PublicOnly {
		t.Errorf("pub: run=%v opts=%+v; want run, public only", g.run["pub"], g.opts["pub"])
	}
	if g.run["envy"] || g.run["lan"] || g.run["evil"] {
		t.Errorf("run = %v; envy, lan and evil must wait", g.run)
	}
	if _, ok := g.opts["envy"]; ok {
		t.Error("a skipped server got options")
	}
	g = a.gate(a.cat.Servers, map[string]bool{"pub": true})
	if !g.run["pub"] || g.opts["pub"].PublicOnly {
		t.Errorf("pub selected: run=%v opts=%+v; a check lifts the restriction", g.run["pub"], g.opts["pub"])
	}
}

// --trust-catalog records the trust for this project, keyed by the hash of
// the catalog file, after printing what it trusts: the file and each remote
// with its URL and header names, a literal value masked and a placeholder
// shown. From then on a plain measure contacts every repository remote,
// while the stdio command still waits for --trust. An edit to the file
// lapses the trust and the rows say so.
func TestTrustCatalogFlagRecordsAndLapses(t *testing.T) {
	marker, root, run := catalogWorkspace(t)
	_, errw, err := run("--trust-catalog", "measure")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--trust-catalog trusts", ".mcp.yaml", "envy: http://127.0.0.1:1/mcp  headers: Authorization: \"Bearer ${MCPICK_REVIEW_TOKEN}\", X-Plain: ****",
		"lan: http://127.0.0.1:1/mcp", "pub: https://mcp.example.invalid/mcp  headers: Authorization: ****",
		"trusted this repository's catalog for " + root, "stdio commands still need --trust"} {
		if !strings.Contains(errw, want) {
			t.Errorf("stderr lacks %q:\n%s", want, errw)
		}
	}
	if strings.Contains(errw, "literal-secret") || strings.Contains(errw, "literal-value") {
		t.Errorf("stderr printed a literal header value:\n%s", errw)
	}
	if strings.Contains(errw, "not measured, defined by this repository") {
		t.Errorf("stderr still reports repository remotes skipped:\n%s", errw)
	}
	if ran(marker) || !strings.Contains(errw, "not trusted") {
		t.Fatalf("the stdio command must still wait for --trust; ran=%v stderr=%q", ran(marker), errw)
	}

	file := filepath.Join(root, ".mcp.yaml")
	hash, _ := trust.CatalogHash(snapshot(t, root))
	path := filepath.Join(os.Getenv("MCPICK_HOME"), "state", "trust.json")
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), hash) {
		t.Fatalf("trust.json = %s, %v; the catalog record should carry the hash", data, err)
	}
	if strings.Contains(string(data), "hunter2") || strings.Contains(string(data), "literal-secret") {
		t.Errorf("trust.json holds a value: %s", data)
	}
	store := trust.Open(path)
	if store.Catalog(root, hash) != trust.CatalogTrusted {
		t.Fatal("the store does not read the catalog as trusted")
	}

	// Honoured by a plain measure and by doctor: envy and lan are tried
	// (connection refused), not skipped.
	out, errw, err := run("--json", "measure")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(errw, "defined by this repository") {
		t.Errorf("a plain measure did not honour the stored trust:\n%s", errw)
	}
	for _, n := range []string{"envy", "lan"} {
		if !strings.Contains(out, `"name": "`+n+`"`) || strings.Contains(out, n+`",
    "ok": false,
    "error": "skipped`) {
			t.Errorf("%s should be tried under a trusted catalog:\n%s", n, out)
		}
	}
	out, _, _ = run("--select", "envy", "doctor")
	if !strings.Contains(out, "FAIL  envy") || strings.Contains(out, "SKIP  envy") {
		t.Errorf("doctor = %q", out)
	}

	// An edit lapses it.
	if err := os.WriteFile(file, append([]byte("# edited\n"), mustRead(t, file)...), 0o644); err != nil {
		t.Fatal(err)
	}
	_, errw, err = run("measure")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errw, "catalog changed since you trusted it") || !strings.Contains(errw, "envy") {
		t.Errorf("stderr after an edit = %q; the lapse should be said", errw)
	}
	// --trust-catalog again replaces the record with the new hash.
	if _, _, err := run("--trust-catalog", "measure"); err != nil {
		t.Fatal(err)
	}
	edited, _ := trust.CatalogHash(snapshot(t, root))
	store = trust.Open(path)
	if len(store.Catalogs) != 1 || store.Catalog(root, edited) != trust.CatalogTrusted {
		t.Errorf("after re-trusting: %+v", store.Catalogs)
	}
}

// With no catalog file the flag has nothing to trust and says so; it
// records nothing.
func TestTrustCatalogFlagWithoutCatalogFile(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MCPICK_HOME", t.TempDir())
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	var out, errw strings.Builder
	a := &app{out: &out, errw: &errw, version: "test"}
	if err := a.run([]string{"--trust-catalog", "measure"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errw.String(), "nothing to trust") {
		t.Errorf("stderr = %q", errw.String())
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("MCPICK_HOME"), "state", "trust.json")); err == nil {
		data, _ := os.ReadFile(filepath.Join(os.Getenv("MCPICK_HOME"), "state", "trust.json"))
		if strings.Contains(string(data), `"hash"`) {
			t.Errorf("a record was written: %s", data)
		}
	}
	if len(snapshot(t, root)) != 0 {
		t.Error("the test root should hold no catalog file")
	}
}

// snapshot is the catalog files under root as they are on disk now.
func snapshot(t *testing.T, root string) []catalog.File {
	t.Helper()
	files, err := catalog.Snapshot(catalog.Path(root), root)
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// --trust-catalog records the hash of the bytes the catalog was parsed from
// — what it lists on stderr — and not of the file as it is on disk when the
// trust is written. A file edited between the two would otherwise be
// trusted unseen: the listing says one catalog, the record covers another.
func TestTrustCatalogFlagHashesWhatItParsed(t *testing.T) {
	_, root, _ := catalogWorkspace(t)
	var out, errw strings.Builder
	a := &app{out: &out, errw: &errw, version: "test", root: root, cwd: root, opt: options{trustCatalog: true}}
	var err error
	if a.cat, err = catalog.Load(catalog.Path(root), root, root); err != nil {
		t.Fatal(err)
	}
	shown, _ := trust.CatalogHash(a.cat.Files)
	file := filepath.Join(root, ".mcp.yaml")
	if err := os.WriteFile(file, []byte("servers:\n  sneaky:\n    type: http\n    url: http://127.0.0.1:1/mcp\n    headers: {Authorization: \"Bearer ${MCPICK_REVIEW_TOKEN}\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	onDisk, _ := trust.CatalogHash(snapshot(t, root))
	if onDisk == shown {
		t.Fatal("the edit did not change the file's hash")
	}

	g := a.gate(a.cat.Servers, map[string]bool{})
	if !g.run["envy"] || !g.run["lan"] {
		t.Errorf("run = %v; the parsed remotes should be measured under the trust", g.run)
	}
	store := trust.Open(filepath.Join(os.Getenv("MCPICK_HOME"), "state", "trust.json"))
	if len(store.Catalogs) != 1 || store.Catalogs[0].Hash != shown {
		t.Fatalf("recorded %+v, want the hash of the parsed bytes %s", store.Catalogs, shown)
	}
	if store.Catalog(root, onDisk) != trust.CatalogChanged {
		t.Error("the edited file on disk must not read as trusted")
	}
	if !strings.Contains(errw.String(), "envy:") || strings.Contains(errw.String(), "sneaky") {
		t.Errorf("stderr should list the parsed catalog, not the disk:\n%s", errw.String())
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// --all and a catalog profile select every server the repository lists, and
// launching with either stays the consent to launch; but neither is a check
// on a repository remote, so measuring under them contacts only the harmless
// ones. A --select, a personal profile or the picker's check is. doctor
// under --all reports the rest as skipped.
func TestAllAndCatalogProfileAreNotMeasuringConsent(t *testing.T) {
	_, root, run := catalogWorkspace(t)
	file := filepath.Join(root, ".mcp.yaml")
	if err := os.WriteFile(file, append(mustRead(t, file), []byte("profiles:\n  repo: [envy, lan, pub]\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	skipped := func(out, name string) bool {
		return strings.Contains(out, `"name": "`+name+`",
    "ok": false,
    "error": "skipped`)
	}
	for _, args := range [][]string{{"--all", "--json", "measure"}, {"--profile", "repo", "--json", "measure"}} {
		out, errw, err := run(args...)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range []string{"envy", "lan"} {
			if !skipped(out, n) {
				t.Errorf("%v: %s must not be measured on the strength of the flag:\n%s", args, n, out)
			}
		}
		if !strings.Contains(out, `"name": "pub"`) || skipped(out, "pub") {
			t.Errorf("%v: the harmless remote is still measured:\n%s", args, out)
		}
		if !strings.Contains(errw, "not checked") || !strings.Contains(errw, "--all and a catalog profile are not a check") {
			t.Errorf("%v: stderr should say what counts:\n%s", args, errw)
		}
	}
	out, _, err := run("--all", "doctor")
	var code proc.ExitCode
	if !errors.As(err, &code) || code != 1 || !strings.Contains(out, "SKIP  envy") || !strings.Contains(out, "SKIP  lan") {
		t.Errorf("doctor --all = %v\n%s; the exposing remotes should be skipped", err, out)
	}

	// Explicit forms are a check: --select, and a personal profile.
	out, _, _ = run("--select", "envy,lan", "--json", "measure")
	if skipped(out, "envy") || skipped(out, "lan") {
		t.Errorf("--select is consent; got:\n%s", out)
	}
	mine := "profiles:\n  - name: repo\n    servers: [envy, lan]\n"
	if err := os.WriteFile(filepath.Join(os.Getenv("MCPICK_HOME"), "profiles.yaml"), []byte(mine), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, _ = run("--profile", "repo", "--json", "measure")
	if skipped(out, "envy") || skipped(out, "lan") {
		t.Errorf("a personal profile is consent; got:\n%s", out)
	}
}
