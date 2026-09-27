package e2e

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Dockerfile.dockerignore is the only thing between a developer's working
// tree and the image's build cache: the owner's rule is that nothing
// security-related from this machine or the project enters the container.
// Docker's own matcher decides what a pattern means, so the check builds a
// throwaway context with the real ignore file, secret-shaped decoys and the
// files the build needs, and reads back what got through. It needs Docker,
// as the image does; `just e2e` runs it before the build.
func TestBuildContextAdmitsOnlySource(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not installed")
	}
	if out, err := exec.Command("docker", "buildx", "version").CombinedOutput(); err != nil {
		t.Skipf("docker buildx unavailable: %v: %s", err, strings.TrimSpace(string(out)))
	}
	ignore, err := os.ReadFile("Dockerfile.dockerignore")
	if err != nil {
		t.Fatal(err)
	}

	source := []string{
		"go.mod", "go.sum", "main.go",
		"internal/backend/claude.go", "internal/backend/claude_test.go", "internal/tui/tui.go",
		"e2e/go.mod", "e2e/fakemcp/main.go", "e2e/runner/main.go", "e2e/runner/pgroup_unix.go",
	}
	// Names only; the contents are a placeholder. Each stands for a class of
	// file a working tree may hold: environment files, catalogs and agent
	// settings with inline credentials, keys, package-manager logins, the
	// repository history, built output, and stray non-Go files next to the
	// source.
	decoys := []string{
		".env", ".env.local", "e2e/.env", "internal/backend/.env",
		".mcp.yaml", ".mcp.json", "examples/.mcp.yaml",
		".claude/settings.local.json", ".claude.json", ".mcpick/state/trust.json",
		".ssh/id_ed25519", "id_rsa", "internal/backend/key.pem", "e2e/runner/token.json",
		".npmrc", ".netrc",
		".git/config", "dist/mcpick", ".mcp.yaml.mcpick-bak-1",
		"README.md", "justfile", "e2e/Dockerfile", "e2e/Dockerfile.dockerignore",
	}

	rootIgnore, err := os.ReadFile(filepath.Join("..", ".dockerignore"))
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.TempDir()
	for _, name := range append(append([]string{}, source...), decoys...) {
		put(t, ctx, name, []byte("placeholder\n"))
	}
	// The real layout: the repository's own .dockerignore, a backstop that
	// admits nothing, at the root of the context, and the allowlist under
	// the name BuildKit looks for next to the Dockerfile it is given, which
	// takes precedence. The decoys of the same names are overwritten.
	put(t, ctx, ".dockerignore", rootIgnore)
	put(t, ctx, "e2e/Dockerfile.dockerignore", ignore)
	put(t, ctx, "e2e/Dockerfile", []byte("FROM scratch\nCOPY . /ctx\n"))
	got := contextThrough(t, ctx, "e2e/Dockerfile")

	// Any other Dockerfile pointed at the repository, with no ignore file
	// of its own, gets the backstop: an empty context.
	put(t, ctx, "Dockerfile", []byte("FROM scratch\nCOPY . /ctx\n"))
	if other := contextThrough(t, ctx, "Dockerfile"); len(other) != 0 {
		t.Errorf("the root .dockerignore let %v into another Dockerfile's context", other)
	}

	sort.Strings(got)
	sort.Strings(source)
	for _, name := range got {
		if i := sort.SearchStrings(source, name); i == len(source) || source[i] != name {
			t.Errorf("%s entered the build context", name)
		}
	}
	for _, name := range source {
		if i := sort.SearchStrings(got, name); i == len(got) || got[i] != name {
			t.Errorf("%s is needed by the build and did not enter", name)
		}
	}
}

func put(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// contextThrough builds dockerfile in ctx and returns the files that
// reached the build, as slash-separated paths relative to the context.
func contextThrough(t *testing.T, ctx, dockerfile string) []string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "out")
	build := exec.Command("docker", "buildx", "build", "-q", "-f", dockerfile, "--output", "type=local,dest="+out, ".")
	build.Dir = ctx
	if msg, err := build.CombinedOutput(); err != nil {
		t.Fatalf("docker build -f %s: %v\n%s", dockerfile, err, msg)
	}
	var got []string
	root := filepath.Join(out, "ctx")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			rel, _ := filepath.Rel(root, path)
			got = append(got, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return got
}
