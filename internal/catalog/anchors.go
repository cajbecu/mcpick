package catalog

import (
	"bytes"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/cajbecu/mcpick/internal/fsutil"
)

// A YAML catalog may share parts between servers with anchors and aliases
// (`&common` on one server, `<<: *common` or `*common` in another). The
// catalog is edited as a node tree and encoded again, which keeps them —
// unless the edit removes the node an anchor is on while an alias elsewhere
// still points at it: the file written would then name an anchor nothing
// defines, which no YAML reader accepts, and every server in it would be
// lost to the next load. Such an edit is refused before anything is written
// (anchoredElsewhere), and every write is read back before it replaces the
// file (encodeChecked), so a case the first check misses still cannot
// corrupt the catalog.

// anchoredElsewhere names the anchors defined inside the nodes about to be
// removed that an alias outside them still uses, sorted; nil when removing
// them leaves every alias in doc with its anchor.
func anchoredElsewhere(doc *yaml.Node, removed ...*yaml.Node) []string {
	inside := map[*yaml.Node]bool{}
	anchored := map[*yaml.Node]bool{}
	for _, n := range removed {
		walkYAML(n, func(m *yaml.Node) bool {
			inside[m] = true
			if m.Anchor != "" {
				anchored[m] = true
			}
			return true
		})
	}
	if len(anchored) == 0 {
		return nil
	}
	var used []string
	walkYAML(doc, func(m *yaml.Node) bool {
		if inside[m] {
			return false
		}
		if m.Kind == yaml.AliasNode && anchored[m.Alias] && !slices.Contains(used, m.Alias.Anchor) {
			used = append(used, m.Alias.Anchor)
		}
		return true
	})
	slices.Sort(used)
	return used
}

// walkYAML calls fn on n and, while fn says so, on every node under it. An
// alias is not followed: the node it points at is walked where it is.
func walkYAML(n *yaml.Node, fn func(*yaml.Node) bool) {
	if n == nil || !fn(n) {
		return
	}
	for _, c := range n.Content {
		walkYAML(c, fn)
	}
}

// anchorError is the refusal of an edit that would strand an alias.
func anchorError(path, what string, anchors []string) error {
	refs := make([]string, len(anchors))
	for i, a := range anchors {
		refs[i] = "&" + a
	}
	return fmt.Errorf("%s defines the YAML anchor %s, which the rest of %s still uses; "+
		"edit the file by hand (copy the shared part into the servers that use it) and try again",
		what, strings.Join(refs, ", "), fsutil.ShortenHome(path))
}

// encodeChecked encodes doc and reads the bytes back: they have to parse
// as a catalog, and hold exactly the servers in want, each with the spec
// given (compared as JSON values). Anything else — an alias left without
// its anchor, a server lost or changed by the encoding — is an error, and
// the caller writes nothing.
func encodeChecked(path string, doc *yaml.Node, want map[string]map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	_, got, _, err := parseYAML(path, buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("the edit would leave %s unreadable (%v); nothing was written", fsutil.ShortenHome(path), err)
	}
	for name, sp := range want {
		have, ok := got[name]
		if !ok || !reflect.DeepEqual(canonical(have), canonical(sp)) {
			return nil, fmt.Errorf("the edit would change %s in %s; nothing was written", name, fsutil.ShortenHome(path))
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			return nil, fmt.Errorf("the edit would add %s to %s; nothing was written", name, fsutil.ShortenHome(path))
		}
	}
	return buf.Bytes(), nil
}

// deletable says whether the server named name can be deleted from the
// catalog file at path without stranding an alias; a move asks before it
// writes the destination, so a refusal leaves both files as they were.
func deletable(path, name string) error {
	if strings.HasSuffix(path, ".json") {
		return nil
	}
	doc, _, err := readYAML(path)
	if err != nil || doc == nil {
		return err
	}
	servers, _, err := serversBlock(doc.Content[0], path)
	if err != nil {
		return err
	}
	if used := anchoredElsewhere(doc, mapEntry(servers, name)...); len(used) > 0 {
		return anchorError(path, name, used)
	}
	return nil
}
