package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/cajbecu/mcpick/internal/fsutil"
)

// Hidden is the set of servers the user hid in the picker (h). It is personal
// and per workspace — a preference of one person, so never written into the
// repository's catalog — and lives under mcpick's state directory.
type Hidden struct {
	Names     []string `json:"hidden"`
	Updated   string   `json:"updated"`
	Workspace string   `json:"workspace,omitempty"`
}

// HiddenPath is the file holding the hidden set for the workspace at root:
//
//	~/.mcpick/state/hidden/<workspace name>-<hash>.json
//
// It is named like the workspace's selections directory but kept apart from
// it, so no --uid can ever share a file name with it (a uid is a file
// *inside* that directory, and file systems that fold case would otherwise
// let "HIDDEN" reach hidden.json).
func HiddenPath(root string) string {
	return filepath.Join(fsutil.StateDir(), "hidden", filepath.Base(Dir(root))+".json")
}

// LoadHidden returns the hidden set, empty when nothing was hidden yet. A
// corrupt file reads as empty, like a corrupt selection: the picker still
// opens, with every server in view.
func LoadHidden(root string) (map[string]bool, error) {
	data, err := os.ReadFile(HiddenPath(root))
	if os.IsNotExist(err) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, err
	}
	var h Hidden
	set := map[string]bool{}
	if json.Unmarshal(data, &h) != nil {
		return set, nil
	}
	for _, n := range h.Names {
		set[n] = true
	}
	return set, nil
}

// SaveHidden writes the hidden set for the workspace at root, replacing
// whatever was there. Pickers change one server at a time and may run side
// by side; they go through SetHidden so neither overwrites the other's
// changes with its own stale copy.
func SaveHidden(root string, set map[string]bool) error {
	h := Hidden{
		Names:     make([]string, 0, len(set)),
		Updated:   time.Now().UTC().Format(time.RFC3339),
		Workspace: root,
	}
	for n, on := range set {
		if on {
			h.Names = append(h.Names, n)
		}
	}
	sort.Strings(h.Names)
	data, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	return fsutil.WriteFileAtomic(HiddenPath(root), append(data, '\n'), 0o600)
}

// SetHidden hides or unhides one server for the workspace at root and
// returns the set as it now stands on disk. The read-modify-write runs under
// a lock beside the file, so two pickers open on the same project each keep
// the other's changes instead of the last one to save winning.
func SetHidden(root, name string, hide bool) (map[string]bool, error) {
	path := HiddenPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	unlock, err := fsutil.Lock(path, 2*time.Second, 30*time.Second)
	if err != nil {
		return nil, err
	}
	defer unlock()
	set, err := LoadHidden(root)
	if err != nil {
		return nil, err
	}
	if hide {
		set[name] = true
	} else {
		delete(set, name)
	}
	if err := SaveHidden(root, set); err != nil {
		return nil, err
	}
	return set, nil
}
