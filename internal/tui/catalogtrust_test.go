package tui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/cajbecu/mcpick/internal/catalog"
	"github.com/cajbecu/mcpick/internal/mcp"
	"github.com/cajbecu/mcpick/internal/state"
	"github.com/cajbecu/mcpick/internal/trust"
)

// catalogPicker is the trust picker over a real catalog file: a temporary
// project root holding .mcp.yaml, and three repository remotes — the sample
// github with its `${GITHUB_TOKEN}` header, intranet on a private address,
// and pub, a public URL with a literal Authorization header, which is the
// harmless kind. Nothing is checked. Cursor on github.
func catalogPicker(t *testing.T) (*picker, string) {
	t.Helper()
	p := trustPicker(t)
	root := t.TempDir()
	file := filepath.Join(root, ".mcp.yaml")
	err := os.WriteFile(file, []byte("servers:\n  pub:\n    url: https://mcp.example.com/mcp\n"), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	p.tr.root, p.cat.Path = root, file
	if p.cat.Files, err = catalog.Snapshot(file, root); err != nil {
		t.Fatal(err)
	}
	p.sel = map[string]bool{}
	p.cursor = 0
	repo := []catalog.Server{
		{Name: "intranet", Origin: catalog.OriginProject, Spec: httpSpec("http://10.0.0.5/mcp")},
		{Name: "pub", Origin: catalog.OriginProject, Spec: map[string]any{"type": "http", "url": "https://mcp.example.com/mcp",
			"headers": map[string]any{"Authorization": "Bearer literal-xyz"}}},
	}
	p.cat.Servers = append(append(append([]catalog.Server{}, p.cat.Servers[:2]...), repo...), p.cat.Servers[2:]...)
	return p, file
}

// headingOf is the group heading line for label (rowOf is for server rows).
func headingOf(frame, label string) string {
	for _, l := range strings.Split(plain(frame), "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), label+" ") {
			return l
		}
	}
	return ""
}

// A bare m measures the repository remote that sends nothing of the user's
// — literal URL, literal header, public host — with the public-only
// restriction, and leaves the others to a check, each row saying which
// condition keeps it out.
func TestHarmlessRepositoryRemoteIsMeasuredPublicOnly(t *testing.T) {
	p, _ := catalogPicker(t)
	press(p, "m")
	if !p.measuring["pub"] {
		t.Fatal("a harmless repository remote was not measured")
	}
	if p.measuring["github"] || p.measuring["intranet"] {
		t.Fatalf("a repository remote that exposes something was measured: %v", p.measuring)
	}
	if v := trust.Gate(p.scope(), p.cat.Servers[3], false); !v.PublicOnly {
		t.Error("the harmless measurement must carry the public-only restriction")
	}
	frame := plain(p.View().Content)
	for _, n := range []string{"github", "intranet"} {
		if row := rowOf(frame, n); !strings.Contains(row, "ask") {
			t.Errorf("%s row = %q, want ask", n, row)
		}
	}
	if !strings.Contains(p.status, "skipped 4") || !strings.Contains(p.status, "T trusts the repo") {
		t.Errorf("status = %q", p.status)
	}
	press(p, "down") // the status line gives way to the row's detail once the cursor moves
	press(p, "up")
	p.width = 140 // room for the condition after the hint
	if frame = plain(p.View().Content); !strings.Contains(frame, "github: tick it (space), or T to trust the repo · repository server: sends ${GITHUB_TOKEN} to its host") {
		t.Errorf("the detail line should say what lifts it and name the placeholder:\n%s", frame)
	}
	press(p, "down") // onto local-tool, then intranet
	press(p, "down")
	if frame = plain(p.View().Content); !strings.Contains(frame, "intranet: tick it (space), or T to trust the repo · repository server: private address") {
		t.Errorf("the detail line should say private address:\n%s", frame)
	}
}

// T shows the catalog files and every remote the trust would cover — URL
// and header names, a literal value masked, a placeholder shown — and says
// commands are not covered. y records the trust with the files' hash,
// measures the rows that were waiting, and from then on a bare m measures
// every repository remote without the restriction, while the stdio command
// still asks. The Project heading says trusted.
func TestTrustCatalogFlow(t *testing.T) {
	p, file := catalogPicker(t)
	widen(p, file)
	press(p, "m")
	press(p, "T")
	if p.mode != modeConfirmCatalog {
		t.Fatalf("mode = %v, want the catalog screen", p.mode)
	}
	frame := plain(p.View().Content)
	for _, want := range []string{"Trust this repository's catalog?", ".mcp.yaml", "github", "Authorization: \"Bearer ${GITHUB_TOKEN}\"",
		"intranet", "http://10.0.0.5/mcp", "pub", "Authorization: ****", "Stdio commands are not covered"} {
		if !strings.Contains(frame, want) {
			t.Errorf("screen lacks %q:\n%s", want, frame)
		}
	}
	if strings.Contains(frame, "literal-xyz") || strings.Contains(frame, "local-tool") {
		t.Errorf("the screen shows a literal header value or a command:\n%s", frame)
	}
	press(p, "esc")
	if p.mode != modeList || len(p.tr.store.Catalogs) != 0 {
		t.Fatal("esc must record nothing")
	}

	press(p, "T")
	press(p, "y")
	if p.mode != modeList {
		t.Fatal("y should return to the list")
	}
	hash, _ := trust.CatalogHash(p.cat.Files)
	if len(p.tr.store.Catalogs) != 1 || p.tr.store.Catalogs[0].Hash != hash || p.tr.store.Catalogs[0].Root != p.tr.root {
		t.Fatalf("catalog grants = %+v, want one with hash %s", p.tr.store.Catalogs, hash)
	}
	data, err := os.ReadFile(p.tr.store.Path())
	if err != nil || !strings.Contains(string(data), hash) {
		t.Errorf("trust.json = %q, %v; the record should be on disk", data, err)
	}
	if !p.measuring["github"] || !p.measuring["intranet"] {
		t.Errorf("y should measure the rows that were waiting for it: %v", p.measuring)
	}
	if p.measuring["local-tool"] {
		t.Error("y trusted a command")
	}
	if !strings.Contains(p.status, "trusted this repository's catalog") {
		t.Errorf("status = %q", p.status)
	}
	frame = plain(p.View().Content)
	if head := headingOf(frame, "Project"); !strings.Contains(head, "trusted") {
		t.Errorf("the Project heading should say trusted: %q", head)
	}
	if head := headingOf(frame, "User"); strings.Contains(head, "trusted") {
		t.Errorf("only the Project heading carries the tag: %q", head)
	}

	// From now on a bare m measures every repository remote, without the
	// restriction; the command still asks.
	p.measuring = map[string]bool{}
	press(p, "m")
	for _, n := range []string{"github", "intranet", "pub"} {
		if !p.measuring[n] {
			t.Errorf("%s not measured under a trusted catalog", n)
		}
		if v := trust.Gate(p.scope(), p.cat.Servers[indexOf(p, n)], false); v.PublicOnly {
			t.Errorf("%s still restricted under a trusted catalog", n)
		}
	}
	if p.measuring["local-tool"] {
		t.Error("the stdio command ran without its own trust")
	}
	if row := rowOf(plain(p.View().Content), "local-tool"); !strings.Contains(row, "run?") {
		t.Errorf("local-tool row = %q, want run?", row)
	}
	if !strings.Contains(p.status, "skipped 2") || strings.Contains(p.status, "repository server") {
		t.Errorf("status = %q; only the two commands should be skipped", p.status)
	}
}

// An edit to the catalog file lapses the trust: once the edited catalog is
// loaded, the rows are back to check, the detail line and the status say
// why, the heading loses its tag, and T asks again.
func TestCatalogEditLapsesTrust(t *testing.T) {
	p, file := catalogPicker(t)
	press(p, "T")
	press(p, "y")
	if err := os.WriteFile(file, []byte("servers:\n  pub:\n    url: https://evil.example.com/mcp\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	reload(t, p)
	p.measuring = map[string]bool{}
	press(p, "m")
	if p.measuring["github"] || p.measuring["intranet"] {
		t.Fatalf("an edited catalog kept its trust: %v", p.measuring)
	}
	if !p.measuring["pub"] {
		t.Error("the harmless remote is still measured on its own merits")
	}
	if !strings.Contains(p.status, "catalog changed since you trusted it; T to trust it again") {
		t.Errorf("status = %q", p.status)
	}
	press(p, "down") // the status line gives way to the row's detail once the cursor moves
	press(p, "up")
	frame := plain(p.View().Content)
	if !strings.Contains(frame, "github: catalog changed since you trusted it; T to trust it again") {
		t.Errorf("the detail line should say the trust lapsed:\n%s", frame)
	}
	if head := headingOf(frame, "Project"); strings.Contains(head, "trusted") {
		t.Errorf("the heading still says trusted: %q", head)
	}
	press(p, "T")
	if frame = plain(p.View().Content); !strings.Contains(frame, "Trust this repository's catalog?") || !strings.Contains(frame, "changed since you trusted it") {
		t.Errorf("T should offer to trust the changed catalog and say it changed:\n%s", frame)
	}
	press(p, "y")
	edited, _ := trust.CatalogHash(p.cat.Files)
	if len(p.tr.store.Catalogs) != 1 || p.tr.store.Catalogs[0].Hash != edited {
		t.Errorf("re-trusting should replace the record: %+v", p.tr.store.Catalogs)
	}
	if !p.measuring["github"] {
		t.Error("the re-trusted rows were not measured")
	}
}

// reload gives the picker the catalog files as they are on disk now, as a
// fresh Run would parse them; the rows are left as they are, since these
// tests only look at the trust.
func reload(t *testing.T, p *picker) {
	t.Helper()
	var err error
	if p.cat.Files, err = catalog.Snapshot(p.cat.Path, p.tr.root); err != nil {
		t.Fatal(err)
	}
	p.refreshCatalog()
}

// The trust T records is keyed on the bytes the picker parsed — the
// catalog on screen — not on the file as it is on disk when y is pressed.
// A file edited while the picker is open is not what the screen shows, so
// T refuses and asks for a reload, whether the edit came before T or while
// the screen was open; nothing is recorded either way. Once reloaded, T
// shows and trusts the edited catalog.
func TestTrustCatalogRefusesAFileChangedOnDisk(t *testing.T) {
	p, file := catalogPicker(t)
	shown, _ := trust.CatalogHash(p.cat.Files)
	edit := func(body string) {
		t.Helper()
		if err := os.WriteFile(file, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	edit("servers:\n  pub:\n    url: https://evil.example.com/mcp\n    headers: {Authorization: \"Bearer ${GITHUB_TOKEN}\"}\n")
	press(p, "T")
	if p.mode != modeList || !strings.Contains(plain(p.status), "the catalog changed on disk; reload to review it") {
		t.Fatalf("mode=%v status=%q; T must refuse a catalog that changed on disk", p.mode, plain(p.status))
	}
	if len(p.tr.store.Catalogs) != 0 {
		t.Fatalf("recorded %+v", p.tr.store.Catalogs)
	}

	// The screen open, then the edit: y refuses too.
	edit("servers:\n  pub:\n    url: https://mcp.example.com/mcp\n")
	press(p, "T")
	if p.mode != modeConfirmCatalog {
		t.Fatalf("mode = %v; with the file as parsed, T should ask", p.mode)
	}
	edit("servers:\n  pub:\n    url: https://evil.example.com/mcp\n")
	press(p, "y")
	if p.mode != modeList || !strings.Contains(plain(p.status), "the catalog changed on disk; reload to review it") {
		t.Fatalf("mode=%v status=%q; y must refuse a catalog that changed under the screen", p.mode, plain(p.status))
	}
	if len(p.tr.store.Catalogs) != 0 || p.measuring["github"] {
		t.Fatalf("y recorded %+v or measured %v", p.tr.store.Catalogs, p.measuring)
	}
	if hash, _ := trust.CatalogHash(p.cat.Files); hash != shown {
		t.Error("the loaded catalog's hash must not follow the disk")
	}

	// Reloaded, the edited catalog is what is shown, and it can be trusted.
	reload(t, p)
	press(p, "T")
	press(p, "y")
	edited, _ := trust.CatalogHash(p.cat.Files)
	if len(p.tr.store.Catalogs) != 1 || p.tr.store.Catalogs[0].Hash != edited || edited == shown {
		t.Errorf("after the reload: %+v, want the edited hash %s", p.tr.store.Catalogs, edited)
	}
}

// T on a trusted catalog offers to untrust it; y removes the record. The key
// line says which T would do: "T trust repo" until trusted, "T untrust repo"
// while it is, "T trust repo" again once untrusted or lapsed.
func TestUntrustCatalog(t *testing.T) {
	p, _ := catalogPicker(t)
	p.width = 200
	if line := p.hint(); !strings.Contains(line, "T trust repo") || strings.Contains(line, "untrust") {
		t.Errorf("before trusting, the key line should offer T trust repo: %s", line)
	}
	press(p, "T")
	press(p, "y")
	if line := p.hint(); !strings.Contains(line, "T untrust repo") {
		t.Errorf("with the catalog trusted, the key line should offer T untrust repo: %s", line)
	}
	press(p, "T")
	frame := plain(p.View().Content)
	if p.mode != modeConfirmCatalog || !strings.Contains(frame, "Untrust this repository's catalog?") || !strings.Contains(frame, "y untrust") {
		t.Fatalf("T on a trusted catalog should offer to untrust it:\n%s", frame)
	}
	press(p, "y")
	if len(p.tr.store.Catalogs) != 0 || p.tr.catalog != trust.CatalogUntrusted {
		t.Errorf("untrust left %+v", p.tr.store.Catalogs)
	}
	data, _ := os.ReadFile(p.tr.store.Path())
	if strings.Contains(string(data), `"hash"`) {
		t.Errorf("trust.json still holds the record: %s", data)
	}
	if !strings.Contains(p.status, "untrusted this repository's catalog") {
		t.Errorf("status = %q", p.status)
	}
	if line := p.hint(); !strings.Contains(line, "T trust repo") || strings.Contains(line, "untrust") {
		t.Errorf("once untrusted, the key line should offer T trust repo again: %s", line)
	}
	if head := headingOf(p.View().Content, "Project"); strings.Contains(head, "trusted") {
		t.Errorf("the heading still says trusted: %q", head)
	}
	p.measuring = map[string]bool{}
	press(p, "m")
	if p.measuring["github"] {
		t.Error("an untrusted catalog still measured the repository's remote")
	}
}

// Without a catalog file there is nothing to trust, and T says so instead
// of opening a screen; likewise a catalog with no remote server.
func TestTrustCatalogNothingToTrust(t *testing.T) {
	p := trustPicker(t)
	p.tr.root, p.cat.Path = t.TempDir(), filepath.Join(t.TempDir(), ".mcp.yaml")
	press(p, "T")
	if p.mode != modeList || !strings.Contains(p.status, "nothing to trust") {
		t.Errorf("mode=%v status=%q", p.mode, p.status)
	}
	p, _ = catalogPicker(t)
	p.cat.Servers = append([]catalog.Server{p.cat.Servers[1]}, p.cat.Servers[4:]...) // local-tool and the user's own
	press(p, "T")
	if p.mode != modeList || !strings.Contains(p.status, "no remote server") {
		t.Errorf("mode=%v status=%q", p.mode, p.status)
	}
}

// The key line teaches T where there is room and drops it first.
func TestHintMentionsTrustCatalog(t *testing.T) {
	p := newPicker(t)
	p.width = 200
	if line := p.hint(); !strings.Contains(line, "T trust repo") {
		t.Errorf("a wide terminal should show T: %s", line)
	}
	p.width = 100
	if line := p.hint(); strings.Contains(line, "T trust repo") && !strings.Contains(line, "v move") {
		t.Errorf("T should be dropped before v: %s", line)
	}
}

// A catalog write of the picker's own — +, d, v into or out of the
// project catalog, a catalog profile's delete — refreshes the bytes the trust is
// keyed on: the trust held lapses, as for any edit, and T then offers the
// catalog the picker has instead of refusing it as changed on disk. The
// project rows carry the spec as the file has it, so a check's
// fingerprint is the one the next load computes.
func TestPickerWriteRefreshesSnapshot(t *testing.T) {
	p, yaml, _ := movePicker(t)
	p.tr = newTrustState(trust.Open(filepath.Join(t.TempDir(), "trust.json")), p.root)
	reload(t, p)
	at := fakeClock(p)
	key := func(keys ...string) { // a second apart: typed, not pasted
		for _, k := range keys {
			*at = at.Add(time.Second)
			p.Update(keyMsg(k))
		}
	}
	onDisk := func() string {
		t.Helper()
		now, err := catalog.Snapshot(yaml, p.root)
		if err != nil {
			t.Fatal(err)
		}
		hash, _ := trust.CatalogHash(now)
		return hash
	}
	written := func(name string) string {
		t.Helper()
		_, specs, _, err := catalog.ReadFile(yaml)
		if err != nil {
			t.Fatal(err)
		}
		return trust.SpecFingerprint(specs[name])
	}
	// After each write: the snapshot is the disk, and the trust given
	// before the write carries over to the new bytes — the user made the
	// edit here and saw it — with the status saying so.
	retrust := func(step string) {
		t.Helper()
		if hash, _ := trust.CatalogHash(p.cat.Files); hash != onDisk() {
			t.Fatalf("%s: cat.Files is not the catalog on disk", step)
		}
		if p.tr.catalog != trust.CatalogTrusted || len(p.tr.store.Catalogs) != 1 || p.tr.store.Catalogs[0].Hash != onDisk() {
			t.Fatalf("%s: catalog trust = %v %+v, want carried over to the disk", step, p.tr.catalog, p.tr.store.Catalogs)
		}
		if !strings.Contains(plain(p.status), "repo trust carried over") {
			t.Fatalf("%s: status = %q, want the carry-over said", step, plain(p.status))
		}
		if saved := trust.Open(p.tr.store.Path()); len(saved.Catalogs) != 1 || saved.Catalogs[0].Hash != onDisk() {
			t.Fatalf("%s: the carried-over trust was not saved: %+v", step, saved.Catalogs)
		}
	}

	key("T", "y")
	if p.tr.catalog != trust.CatalogTrusted {
		t.Fatalf("catalog trust = %v after T y", p.tr.catalog)
	}

	// +: the new row's spec is the file's, so its fingerprint is stable.
	key("+")
	typeText(p, at, time.Second, "added")
	key("enter", "enter") // the type prompt offers http
	typeText(p, at, time.Second, "https://x/added")
	key("enter")
	if p.mode != modeList || !strings.HasPrefix(p.status, "added added") {
		t.Fatalf("add: mode=%v status=%q", p.mode, plain(p.status))
	}
	if s, ok := p.cat.Find("added"); !ok || s.Source != yaml || trust.SpecFingerprint(s.Spec) != written("added") {
		t.Errorf("added = %+v; want the spec as written to %s", s, yaml)
	}
	retrust("add")

	// v in: gl comes into the project catalog with its credential redacted; the
	// row then holds the redacted spec as the file has it.
	p.cursor = rowIndex(p, "gl")
	key("v", "p")
	if p.mode != modeMoveSecrets {
		t.Fatalf("move: mode = %v, want the credentials question", p.mode)
	}
	key("r")
	if s, ok := p.cat.Find("gl"); !ok || s.Origin != catalog.OriginProject || trust.SpecFingerprint(s.Spec) != written("gl") {
		t.Errorf("gl = %+v; want the redacted spec as written", s)
	}
	retrust("move in")

	// v out: the row leaves the project catalog and the file.
	p.cursor = rowIndex(p, "added")
	key("v", "u")
	if s, _ := p.cat.Find("added"); s.Origin != catalog.OriginUser {
		t.Fatalf("added = %+v after the move out", s)
	}
	retrust("move out")

	// d: the row and its entry go.
	p.cursor = rowIndex(p, "ws")
	key("d", "y")
	if _, ok := p.cat.Find("ws"); ok || p.mode != modeList {
		t.Fatalf("delete: ws still there, mode=%v status=%q", p.mode, plain(p.status))
	}
	retrust("delete")

	// A catalog profile's delete.
	data, err := os.ReadFile(yaml)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(yaml, append(data, "profiles:\n  review: [gl]\n"...), 0o644); err != nil {
		t.Fatal(err)
	}
	reload(t, p)
	key("T", "y")
	p.cat.Profiles["review"], p.cat.ProfileFiles["review"] = []string{"gl"}, yaml
	p.deleteCatalogProfile("review")
	if !strings.HasPrefix(plain(p.status), "deleted profile") {
		t.Fatalf("profile delete: status=%q", plain(p.status))
	}
	retrust("profile delete")

	// An edit made outside the picker lapses the trust as before, even
	// when a write of the picker's own follows it: the carry-over is for
	// what this picker did, and it is decided on the bytes before the
	// write. Here gl's URL changes on disk, unseen; + then writes the
	// file, and the trust is not carried over.
	data, _ = os.ReadFile(yaml)
	if !strings.Contains(string(data), "https://x/gl") {
		t.Fatalf("the fixture lost gl:\n%s", data)
	}
	if err := os.WriteFile(yaml, []byte(strings.Replace(string(data), "https://x/gl", "https://elsewhere/gl", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	key("+")
	typeText(p, at, time.Second, "later")
	key("enter", "enter")
	typeText(p, at, time.Second, "https://x/later")
	key("enter")
	if _, ok := p.cat.Find("later"); !ok || p.mode != modeList {
		t.Fatalf("add after an outside edit: mode=%v status=%q", p.mode, plain(p.status))
	}
	if p.tr.catalog != trust.CatalogChanged || strings.Contains(plain(p.status), "carried over") {
		t.Errorf("trust = %v status=%q; an outside edit must lapse the trust, picker write or not", p.tr.catalog, plain(p.status))
	}
	if saved := trust.Open(p.tr.store.Path()); len(saved.Catalogs) != 1 || saved.Catalogs[0].Hash == onDisk() {
		t.Errorf("the store was re-trusted over an outside edit: %+v", saved.Catalogs)
	}

	// A file that someone else changed under the picker's write is not
	// taken over: the rows would no longer be what the files hold.
	before, _ := trust.CatalogHash(p.cat.Files)
	if err := os.WriteFile(yaml, []byte("servers:\n  other:\n    type: http\n    url: https://x/other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p.resnapshot(nil, true)
	if hash, _ := trust.CatalogHash(p.cat.Files); hash != before {
		t.Error("a file with other servers than the rows must not become the snapshot")
	}
	key("T")
	if p.mode != modeList || !strings.Contains(plain(p.status), catalogChangedOnDisk) {
		t.Errorf("T should refuse: mode=%v status=%q", p.mode, plain(p.status))
	}
}

// Once the last answer is in, the status says measured — and still what
// the press left out: "measured" alone read as if everything had been.
func TestMeasuredKeepsTheSkipNote(t *testing.T) {
	p, _ := catalogPicker(t)
	p.hidden = map[string]bool{"sentry": true}
	press(p, "m")
	if !p.measuring["pub"] || !strings.Contains(p.status, "measuring 5 server(s)… · skipped 4") {
		t.Fatalf("status = %q, measuring %v", p.status, p.measuring)
	}
	for _, name := range []string{"pub", "browser", "linear", "notion", "playwright-mcp"} {
		p.Update(measuredMsg{name: name, spec: p.cat.SpecOf(name), res: mcp.Result{Name: name, OK: true, Tokens: 10}})
	}
	want := "measured · skipped 4: 2 command(s) to review (m m, or M for all), 2 repository remote(s) to tick (or T trusts the repo) · 1 hidden left out (H unfolds them)"
	if plain(p.status) != want {
		t.Errorf("status = %q\n    want %q", plain(p.status), want)
	}
}

// runAll runs a command and every command a batch holds, for tests that
// need the side effects (a measurement's requests), not the messages.
func runAll(c tea.Cmd) {
	if c == nil {
		return
	}
	if b, ok := c().(tea.BatchMsg); ok {
		for _, x := range b {
			runAll(x)
		}
	}
}

// An outside edit to a checked repository server — a git pull in another
// terminal pointing it elsewhere, with the user's token in a header — is
// not adopted by the next write of the picker's own (+ on another server):
// the row keeps the spec the user saw, its check still names that spec so
// the next load finds it unconfirmed, the status asks for a reload, and
// measuring the checked row never sends the token to the new address.
func TestPickerWriteDoesNotAdoptAnOutsideEdit(t *testing.T) {
	var mu sync.Mutex
	var auth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auth = append(auth, r.Header.Get("Authorization"))
		mu.Unlock()
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv("GITHUB_TOKEN", "sekret-123")
	p, yaml, _ := movePicker(t)
	p.tr = newTrustState(trust.Open(filepath.Join(t.TempDir(), "trust.json")), p.root)
	at := fakeClock(p)
	key := func(keys ...string) {
		for _, k := range keys {
			*at = at.Add(time.Second)
			p.Update(keyMsg(k))
		}
	}
	loaded, _ := p.cat.Find("ws")
	before := trust.Checks(p.cat.Servers, p.sel)["ws"]

	evil := "servers:\n  ws:\n    type: http\n    url: " + srv.URL + "/evil\n    headers:\n      Authorization: Bearer ${GITHUB_TOKEN}\n"
	if err := os.WriteFile(yaml, []byte(evil), 0o644); err != nil {
		t.Fatal(err)
	}
	key("+")
	typeText(p, at, time.Second, "added")
	key("enter", "enter")
	typeText(p, at, time.Second, "https://x/added")
	key("enter")
	if _, ok := p.cat.Find("added"); !ok {
		t.Fatalf("the add did not happen: %q", plain(p.status))
	}
	if !strings.Contains(plain(p.status), catalogChangedOnDisk) {
		t.Errorf("status = %q; want it to say the catalog changed on disk", plain(p.status))
	}
	ws, _ := p.cat.Find("ws")
	if trust.SpecFingerprint(ws.Spec) != trust.SpecFingerprint(loaded.Spec) {
		t.Fatalf("ws row took the outside spec: %v", ws.Spec)
	}
	after := trust.Checks(p.cat.Servers, p.sel)["ws"]
	if after != before {
		t.Errorf("ws check moved from %v to %v", before, after)
	}
	cat, err := catalog.Load(yaml, p.root, p.root)
	if err != nil {
		t.Fatal(err)
	}
	if _, unc := trust.Confirm(cat.Servers, []string{"ws"}, map[string]state.Check{"ws": after}); len(unc) != 1 {
		t.Errorf("the next load confirms the outside spec: unconfirmed = %v", unc)
	}
	runAll(p.measureServers(map[string]bool{"ws": true}))
	mu.Lock()
	defer mu.Unlock()
	if strings.Contains(strings.Join(auth, ","), "sekret-123") {
		t.Errorf("the token reached the outside URL: %v", auth)
	}
	key("T")
	if p.mode != modeList || !strings.Contains(plain(p.status), catalogChangedOnDisk) {
		t.Errorf("T should refuse: mode=%v status=%q", p.mode, plain(p.status))
	}
}

// An outside edit that lands between the carry-over's check of the disk
// and the picker's own write is not carried: the files after the write
// hold more than the write, so the trust is neither recorded for them nor
// held for the session.
func TestCarryOverRefusesAnEditInTheWriteWindow(t *testing.T) {
	p, yaml, _ := movePicker(t)
	p.tr = newTrustState(trust.Open(filepath.Join(t.TempDir(), "trust.json")), p.root)
	reload(t, p)
	at := fakeClock(p)
	for _, k := range []string{"T", "y"} {
		*at = at.Add(time.Second)
		p.Update(keyMsg(k))
	}
	if p.tr.catalog != trust.CatalogTrusted {
		t.Fatal("not trusted")
	}
	trusted := p.tr.hash
	note, err := p.editCatalog(func() ([]string, error) {
		if err := os.WriteFile(yaml, []byte("servers:\n  ws:\n    type: http\n    url: https://evil.example/ws\n    headers:\n      X: ${GITHUB_TOKEN}\n"), 0o644); err != nil {
			return nil, err
		}
		sp := map[string]any{"type": "http", "url": "https://x/a"}
		if err := catalog.AddServer(p.cat.Path, "added", sp); err != nil {
			return nil, err
		}
		p.cat.Servers = append(p.cat.Servers, catalog.Server{Name: "added", Origin: catalog.OriginProject, Spec: sp})
		return []string{"added"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(note, "carried over") || !strings.Contains(note, catalogChangedOnDisk) {
		t.Errorf("note = %q", note)
	}
	saved := trust.Open(p.tr.store.Path())
	if len(saved.Catalogs) != 1 || saved.Catalogs[0].Hash != trusted {
		t.Errorf("the store was re-trusted over the outside edit: %+v", saved.Catalogs)
	}
	p.refreshCatalog()
	if p.tr.catalog == trust.CatalogTrusted {
		t.Error("the session still holds the catalog trust over files it did not show")
	}
	if ws, _ := p.cat.Find("ws"); ws.Spec["url"] != "https://x/ws" {
		t.Errorf("ws = %v", ws.Spec)
	}
}
