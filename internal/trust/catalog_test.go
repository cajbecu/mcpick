package trust

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cajbecu/mcpick/internal/catalog"
)

// snapshot is the catalog files under root as they are on disk now, the
// way Load reads them; the hash tests key on it.
func snapshot(t *testing.T, root string) []catalog.File {
	t.Helper()
	files, err := catalog.Snapshot(filepath.Join(root, ".mcp.yaml"), root)
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// The catalog trust is keyed on the files' contents: recorded with the
// hash, in force while the files are as they were, lapsed the moment one
// changes or another appears, gone when withdrawn — and it survives a
// reload of the store.
func TestCatalogTrustFollowsTheFiles(t *testing.T) {
	root := t.TempDir()
	yaml := filepath.Join(root, ".mcp.yaml")
	js := filepath.Join(root, ".mcp.json")
	if err := os.WriteFile(yaml, []byte("servers:\n  a:\n    url: https://a.example.com/mcp\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hash, existing := CatalogHash(snapshot(t, root))
	if len(hash) != 64 || len(existing) != 1 || existing[0] != yaml {
		t.Fatalf("CatalogHash = %q, %v; want a sha256 over the one file that exists", hash, existing)
	}
	if h, e := CatalogHash(nil); h != "" || e != nil {
		t.Errorf("CatalogHash over no file = %q, %v; want nothing", h, e)
	}

	path := filepath.Join(t.TempDir(), "trust.json")
	s := Open(path)
	if s.Catalog(root, hash) != CatalogUntrusted {
		t.Fatal("nothing was trusted yet")
	}
	s.TrustCatalog(root, "", nil) // no file, nothing to trust
	if len(s.Catalogs) != 0 {
		t.Fatal("an empty hash was recorded")
	}
	s.TrustCatalog(root, hash, existing)
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	s = Open(path)
	if s.Catalog(root, hash) != CatalogTrusted {
		t.Fatal("the trust did not survive a reload")
	}
	if s.Catalog("/elsewhere", hash) != CatalogUntrusted {
		t.Error("trust in one project applied in another")
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), hash) || !strings.Contains(string(data), ".mcp.yaml") {
		t.Errorf("trust.json = %s; the record should carry the hash and the file", data)
	}

	// An edit lapses it.
	if err := os.WriteFile(yaml, []byte("servers:\n  a:\n    url: https://evil.example.com/mcp\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	edited, _ := CatalogHash(snapshot(t, root))
	if edited == hash {
		t.Fatal("the hash did not change with the file")
	}
	if s.Catalog(root, edited) != CatalogChanged {
		t.Error("an edited catalog should read as changed, not untrusted or trusted")
	}
	// Trusting again replaces the record rather than adding one.
	s.TrustCatalog(root, edited, existing)
	if len(s.Catalogs) != 1 || s.Catalog(root, edited) != CatalogTrusted || s.Catalog(root, hash) != CatalogChanged {
		t.Errorf("after re-trusting: %+v", s.Catalogs)
	}

	// A second file appearing changes the hash too: it is part of the catalog.
	if err := os.WriteFile(js, []byte(`{"mcpServers":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	both, existing := CatalogHash(snapshot(t, root))
	if both == edited || len(existing) != 2 {
		t.Errorf("a new catalog file did not change the hash: %v", existing)
	}
	if s.Catalog(root, both) != CatalogChanged {
		t.Error("a catalog file that appeared should lapse the trust")
	}

	s.WithdrawCatalog(root)
	if s.Catalog(root, edited) != CatalogUntrusted || len(s.Catalogs) != 0 {
		t.Error("withdraw left a record")
	}
	var nilStore *Store
	nilStore.TrustCatalog(root, hash, existing) // must not panic
	if nilStore.Catalog(root, hash) != CatalogUntrusted {
		t.Error("a nil store trusts nothing")
	}
}

// The hash covers the bytes, the name and the boundaries between files, so
// the same text moved from one file to the other, or split differently,
// does not pass for the catalog that was trusted.
func TestCatalogHashDistinguishesFiles(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, ".mcp.yaml"), filepath.Join(dir, ".mcp.json")
	os.WriteFile(a, []byte("x"), 0o644)
	h1, _ := CatalogHash(snapshot(t, dir))
	os.Remove(a)
	os.WriteFile(b, []byte("x"), 0o644)
	h2, _ := CatalogHash(snapshot(t, dir))
	if h1 == h2 {
		t.Error("the same bytes in another file hashed the same")
	}
	os.WriteFile(a, []byte("x"), 0o644)
	os.WriteFile(b, []byte(""), 0o644)
	h3, _ := CatalogHash(snapshot(t, dir))
	if h3 == h1 {
		t.Error("an empty second file hashed like no second file")
	}
}

// The hash is over bytes handed to it, not over the disk: the same paths
// with other bytes hash differently, and the disk is never consulted. This
// is what lets a trust cover exactly the catalog that was parsed and shown.
func TestCatalogHashIsOverTheBytesGiven(t *testing.T) {
	shown := []catalog.File{{Path: "/repo/.mcp.yaml", Data: []byte("servers:\n  a:\n    url: https://a.example.com/mcp\n")}}
	edited := []catalog.File{{Path: "/repo/.mcp.yaml", Data: []byte("servers:\n  a:\n    url: https://evil.example.com/mcp\n")}}
	h1, paths := CatalogHash(shown)
	h2, _ := CatalogHash(edited)
	if h1 == "" || h1 == h2 {
		t.Errorf("hashes %q and %q; the bytes decide", h1, h2)
	}
	if len(paths) != 1 || paths[0] != "/repo/.mcp.yaml" {
		t.Errorf("paths = %v", paths)
	}
	if h3, _ := CatalogHash(shown); h3 != h1 {
		t.Error("the hash is not stable")
	}
}
