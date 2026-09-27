package trust

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

// Two pickers share trust.json. One withdraws the catalog trust; the other,
// loaded while it was in force, then trusts a command. The second save
// must carry the grant into the file as it is, not put the withdrawn
// catalog trust back from its stale snapshot.
func TestSaveDoesNotResurrectAWithdrawnTrust(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trust.json")
	root := "/w"
	a := Open(path)
	a.TrustCatalog(root, "h1", []string{"/w/.mcp.yaml"})
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}

	b := Open(path) // a second picker, loaded while the trust is in force
	if b.Catalog(root, "h1") != CatalogTrusted {
		t.Fatal("the second picker should see the trust")
	}

	a.WithdrawCatalog(root)
	if err := a.Save(); err != nil {
		t.Fatal(err)
	}
	b.Grant(root, "x", stdio("uvx", []any{"x"}, nil))
	if err := b.Save(); err != nil {
		t.Fatal(err)
	}

	got := Open(path)
	if got.Catalog(root, "h1") != CatalogUntrusted {
		t.Error("the withdrawn catalog trust came back from the second picker's stale copy")
	}
	if !got.Trusted(root, "x", stdio("uvx", []any{"x"}, nil)) {
		t.Error("the second picker's grant was not saved")
	}
	if b.Catalog(root, "h1") != CatalogUntrusted {
		t.Error("after saving, the second picker should hold what the file holds")
	}
}

// Every session saves its own mutation into the shared file; none may be
// lost to another's write. The goroutines start together so their saves
// overlap.
func TestConcurrentSavesLoseNoGrant(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trust.json")
	const n = 24
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s := Open(path)
			<-start
			s.Grant("/w", fmt.Sprintf("s%d", i), stdio("uvx", []any{fmt.Sprint(i)}, nil))
			if err := s.Save(); err != nil {
				errs <- err
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	got := Open(path)
	if len(got.Grants) != n {
		t.Errorf("%d grants saved, want %d: concurrent saves lost some", len(got.Grants), n)
	}
}

// A grant made in memory is trusted at once and survives the reload Save
// does; saving twice does not record it twice.
func TestSaveReplaysOnceAndKeepsMemoryCurrent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trust.json")
	sp := stdio("npx", []any{"y"}, nil)
	s := Open(path)
	s.Grant("/w", "y", sp)
	if !s.Trusted("/w", "y", sp) {
		t.Fatal("a grant should be trusted before it is saved")
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	s.Grant("/w", "y", sp) // already trusted: nothing to record
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	if got := Open(path); len(got.Grants) != 1 || !got.Trusted("/w", "y", sp) {
		t.Errorf("grants = %+v, want the one grant once", got.Grants)
	}
}
