// Package state keeps the selection between launches: which servers were
// checked last time, per workspace and per --uid.
package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/cajbecu/mcpick/internal/fsutil"
)

type State struct {
	Selected []string `json:"selected"`
	// Checks records, for each selected server, what was checked: where it
	// came from and a fingerprint of its spec as written
	// (trust.SpecFingerprint). A name alone is not enough for a server the
	// repository's catalog defines: another server can take the name, or
	// the same server can change its URL or headers, and the check must
	// not carry over (trust.Confirm). A selection saved before checks were
	// recorded has none, and its workspace servers are unconfirmed.
	Checks  map[string]Check `json:"checks,omitempty"`
	Updated string           `json:"updated"`
	Target  string           `json:"target,omitempty"`
	// Workspace records which directory the selection belongs to, so a
	// person reading ~/.mcpick/selections can tell without the hash.
	Workspace string `json:"workspace,omitempty"`
}

// Check is what one checked server was when it was checked.
type Check struct {
	Origin string `json:"origin"`
	Spec   string `json:"spec"`
}

// Dir is the directory holding one workspace's selections:
//
//	~/.mcpick/selections/<workspace name>-<hash>/
//
// The name makes it recognisable; the hash of the full path keeps two
// checkouts that share a name apart.
func Dir(root string) string {
	return filepath.Join(fsutil.SelectionsDir(),
		fsutil.Sanitize(filepath.Base(root))+"-"+fsutil.ShortHash(root)[:8])
}

// Path is the file holding the selection for one --uid in one workspace.
func Path(root, uid string) string {
	return filepath.Join(Dir(root), fsutil.Sanitize(uid)+".json")
}

// legacyPaths are where earlier builds kept the selection outside
// ~/.mcpick. They are read when the current path has nothing yet, and the
// next save lands in the new place, so upgrading loses no selection. They
// are never written or removed. None of them is inside the workspace: a
// file there belongs to whoever wrote the repository, and a selection read
// from it would pre-check whatever the repository chose. (The released
// 0.1.0 already wrote to ~/.mcpick/selections; `<root>/.tmp/` was a
// pre-release default.)
func legacyPaths(root, uid string) []string {
	xdg := os.Getenv("XDG_STATE_HOME")
	if xdg == "" {
		xdg = fsutil.Home(".local", "state")
	}
	return []string{filepath.Join(xdg, "mcpick", fsutil.ShortHash(root)+"-"+fsutil.Sanitize(uid)+".json")}
}

// Load returns the saved selection, or an empty one. A legacy file gives
// its names only: no build that wrote one recorded checks, so checks found
// there were not written by mcpick and are dropped.
func Load(root, uid string) (State, error) {
	current := Path(root, uid)
	for _, p := range append([]string{current}, legacyPaths(root, uid)...) {
		data, err := os.ReadFile(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return State{}, err
		}
		var st State
		if json.Unmarshal(data, &st) != nil {
			return State{}, nil // a corrupt selection is not worth failing a launch
		}
		if p != current {
			st.Checks = nil
		}
		return st, nil
	}
	return State{}, nil
}

// Save writes the selection for uid in the workspace at root.
func Save(root, uid string, st State) error {
	if st.Selected == nil {
		st.Selected = []string{}
	}
	st.Updated = time.Now().UTC().Format(time.RFC3339)
	st.Workspace = root
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(Path(root, uid), append(data, '\n'), 0o600)
}
