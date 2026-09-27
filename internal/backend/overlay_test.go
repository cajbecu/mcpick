package backend

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cajbecu/mcpick/internal/spec"
)

// A file the agent wrote into the overlay that cannot be brought back — the
// real directory is not writable here; a config edit that does not parse is
// the other case — must not go with the overlay: it may be a refreshed login
// or a session's history. It is set aside under state/recovered, with its
// path, where nothing prunes it.
func TestOverlayKeepsFilesItCouldNotSyncBack(t *testing.T) {
	if !unixPerms || os.Getuid() == 0 {
		t.Skip("needs a directory this user cannot write")
	}
	src := t.TempDir()
	write(t, filepath.Join(src, "config.toml"), "model = \"a\"\n")
	if err := os.MkdirAll(filepath.Join(src, "sessions"), 0o755); err != nil {
		t.Fatal(err)
	}
	h := overlayBackend{Meta: Meta{Name: "codex"}, env: "CODEX_HOME", dir: func() string { return src }, file: "config.toml", toml: true, dialect: spec.TOML{Agent: "codex", Headers: "http_headers"}}
	ctx := testCtx(t)
	plan, err := h.Plan(ctx, remoteSel(), []string{"codex"})
	if err != nil {
		t.Fatal(err)
	}
	dir := overlayEnv(t, plan, "CODEX_HOME")

	// What the agent does during the session: a new login file, a new
	// history file in a directory it replaced, and an edit to its config
	// that does not parse.
	write(t, filepath.Join(dir, "auth.json"), `{"token":"refreshed"}`)
	if err := os.Remove(filepath.Join(dir, "sessions")); err != nil { // the symlink
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "sessions", "1.jsonl"), "{}\n")
	write(t, filepath.Join(dir, "config.toml"), "model = \"a\"\n[broken\n")

	for _, d := range []string{src, filepath.Join(src, "sessions")} {
		if err := os.Chmod(d, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(d, 0o755) })
	}

	plan.Cleanup()

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("the overlay should be removed once its files are set aside")
	}
	kept, _ := filepath.Glob(filepath.Join(ctx.State, "recovered", "*"))
	if len(kept) != 1 {
		t.Fatalf("recovered = %v, want one directory for this session", kept)
	}
	for rel, want := range map[string]string{
		"auth.json":        `{"token":"refreshed"}`,
		"sessions/1.jsonl": "{}\n",
		"config.toml":      "model = \"a\"\n[broken\n",
	} {
		got, err := os.ReadFile(filepath.Join(kept[0], filepath.FromSlash(rel)))
		if err != nil {
			t.Errorf("%s was not kept: %v", rel, err)
		} else if string(got) != want {
			t.Errorf("%s = %q, want %q", rel, got, want)
		}
	}
	if !strings.HasPrefix(kept[0], filepath.Join(ctx.State, "recovered")) {
		t.Errorf("kept under %s, want the state directory", kept[0])
	}
	if fi, err := os.Stat(kept[0]); err == nil && fi.Mode().Perm() != 0o700 {
		t.Errorf("recovered directory mode = %v, want 0700", fi.Mode().Perm())
	}
}

// When everything syncs back, nothing is set aside.
func TestOverlayRecoversNothingWhenSyncBackWorks(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "config.toml"), "model = \"a\"\n")
	h := overlayBackend{Meta: Meta{Name: "codex"}, env: "CODEX_HOME", dir: func() string { return src }, file: "config.toml", toml: true, dialect: spec.TOML{Agent: "codex", Headers: "http_headers"}}
	ctx := testCtx(t)
	plan, err := h.Plan(ctx, remoteSel(), []string{"codex"})
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(overlayEnv(t, plan, "CODEX_HOME"), "auth.json"), `{"token":"new"}`)
	plan.Cleanup()
	if _, err := os.Stat(filepath.Join(ctx.State, "recovered")); !os.IsNotExist(err) {
		t.Error("nothing failed, nothing should have been set aside")
	}
	if got, _ := os.ReadFile(filepath.Join(src, "auth.json")); string(got) != `{"token":"new"}` {
		t.Errorf("auth.json = %s, want it synced back", got)
	}
}
