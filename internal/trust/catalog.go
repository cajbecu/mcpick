package trust

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"time"

	"github.com/cajbecu/mcpick/internal/catalog"
)

// A project's catalog can be trusted as a whole: then every remote server
// it defines is measured by a bare m, placeholders and private hosts
// included, because the user has seen the files and said so. The trust is
// keyed by the contents of the files, so an edit — a pull, a rebase, a
// commit by someone else — lapses it and the files are shown again. Stdio
// commands are not covered: each still needs its own grant.

// CatalogGrant records that trust. Hash is CatalogHash over Files as they
// were; Files is for a person reading the record.
type CatalogGrant struct {
	Root  string   `json:"root"`
	Hash  string   `json:"hash"`
	Files []string `json:"files"`
	At    string   `json:"at"`
}

// CatalogStatus is where a project's catalog trust stands now.
type CatalogStatus int

const (
	// CatalogUntrusted: never trusted, or withdrawn.
	CatalogUntrusted CatalogStatus = iota
	// CatalogTrusted: trusted, and the files are as they were.
	CatalogTrusted
	// CatalogChanged: trusted once, but a catalog file has changed since
	// (or one appeared, or went); the trust has lapsed until given again.
	CatalogChanged
)

// CatalogHash identifies the catalog files by their contents: SHA-256 over
// each file — its base name, its length and its bytes, in the order given —
// so that a change to any of them, a file appearing or one going, all give
// a different hash. It hashes bytes already read (Catalog.Files, the bytes
// the servers on screen were parsed from; or catalog.Snapshot for the files
// as they are now), never the disk itself: a trust must cover what was
// shown, and a file edited between the two would otherwise be trusted
// unseen. It returns the hash and the files' paths; both empty when there
// is no file.
func CatalogHash(files []catalog.File) (hash string, paths []string) {
	if len(files) == 0 {
		return "", nil
	}
	h := sha256.New()
	for _, f := range files {
		paths = append(paths, f.Path)
		fmt.Fprintf(h, "%s\x00%d\x00", filepath.Base(f.Path), len(f.Data))
		h.Write(f.Data)
	}
	return hex.EncodeToString(h.Sum(nil)), paths
}

// Catalog reports the catalog trust of a project, given the hash of its
// files as they are now. A nil store trusts nothing.
func (s *Store) Catalog(root, hash string) CatalogStatus {
	if s == nil || hash == "" {
		return CatalogUntrusted
	}
	for _, g := range s.Catalogs {
		if g.Root != root {
			continue
		}
		if g.Hash == hash {
			return CatalogTrusted
		}
		return CatalogChanged
	}
	return CatalogUntrusted
}

// TrustCatalog records the trust for a project, replacing a lapsed record;
// the caller saves. Nothing is recorded for an empty hash: no file, nothing
// to trust.
func (s *Store) TrustCatalog(root, hash string, files []string) {
	if s == nil || hash == "" {
		return
	}
	g := CatalogGrant{
		Root: root, Hash: hash, Files: files,
		At: time.Now().UTC().Format(time.RFC3339),
	}
	s.mutate(func(f *Store) {
		f.dropCatalog(root)
		f.Catalogs = append(f.Catalogs, g)
	})
}

// WithdrawCatalog removes a project's catalog trust; the caller saves.
func (s *Store) WithdrawCatalog(root string) {
	if s == nil {
		return
	}
	s.mutate(func(f *Store) { f.dropCatalog(root) })
}

func (s *Store) dropCatalog(root string) {
	kept := s.Catalogs[:0]
	for _, g := range s.Catalogs {
		if g.Root != root {
			kept = append(kept, g)
		}
	}
	s.Catalogs = kept
}
