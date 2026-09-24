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
	Updated  string   `json:"updated"`
	Target   string   `json:"target,omitempty"`
	// Workspace records which directory the selection belongs to, so a
	// person reading ~/.mcpick/selections can tell without the hash.
	Workspace string `json:"workspace,omitempty"`
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

// legacyPaths are where earlier versions kept the selection. They are read
// when the current path has nothing yet, and the next save lands in the new
// place, so upgrading loses no selection. They are never written or removed.
func legacyPaths(root, uid string) []string {
	u := fsutil.Sanitize(uid)
	paths := []string{
		filepath.Join(root, ".tmp", "mcpick-"+u+".json"),        // 0.1.0 default
		filepath.Join(root, ".scratchpad", "mcpick", u+".json"), // the prototype
	}
	xdg := os.Getenv("XDG_STATE_HOME")
	if xdg == "" {
		xdg = fsutil.Home(".local", "state")
	}
	return append(paths, filepath.Join(xdg, "mcpick", fsutil.ShortHash(root)+"-"+u+".json"))
}

// Load returns the saved selection, or an empty one.
func Load(root, uid string) (State, error) {
	for _, p := range append([]string{Path(root, uid)}, legacyPaths(root, uid)...) {
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
