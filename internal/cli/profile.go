package cli

// Profiles on the command line: --profile, `mcpick profile`, and the footer
// of `mcpick list`. Three kinds exist — the user's own, the catalog's and the
// built-in default — and a personal one shadows a catalog one of the same
// name; only personal ones are ever written.

import (
	"fmt"
	"strings"

	"github.com/cajbecu/mcpick/internal/catalog"
	"github.com/cajbecu/mcpick/internal/fsutil"
	"github.com/cajbecu/mcpick/internal/profile"
	"github.com/cajbecu/mcpick/internal/state"
	"github.com/cajbecu/mcpick/internal/trust"
)

const (
	sourcePersonal = "personal"
	sourceCatalog  = "catalog"
	sourceBuiltin  = "built-in"
)

// profiles loads the user's set once. A file that cannot be read is said
// once and listed as empty; nothing is written over it, and --profile NAME
// refuses to resolve while it stays unreadable (profileSelection).
func (a *app) profiles() *profile.Set {
	if a.prof == nil {
		a.prof = profile.Load()
		if err := a.prof.Err(); err != nil {
			a.warnf("warning: %s could not be read (%v); personal profiles are unavailable",
				fsutil.ShortenHome(a.prof.Path()), err)
		}
		for _, w := range a.prof.Warnings {
			a.warnf("warning: %s: %s", fsutil.ShortenHome(a.prof.Path()), w)
		}
		if a.catalogDefault() {
			a.warnf("warning: %s", a.defaultConflict())
		}
	}
	return a.prof
}

// catalogDefault says the catalog defines a profile named like the built-in
// one — a leftover from when profiles lived only in the catalog.
func (a *app) catalogDefault() bool {
	_, ok := a.cat.Profiles[profile.Default]
	return ok
}

// defaultConflict is why --profile default is refused while the catalog has
// its own: the built-in one is everything available, usually more than the
// catalog's lists, and a launch must never run more than the user asked for.
// The message is the migration.
func (a *app) defaultConflict() error {
	return fmt.Errorf("%s defines a profile named %q, the built-in profile (everything available); "+
		"--profile %s is refused so it cannot run more than the catalog's lists: rename it there "+
		"(e.g. base) or copy it into your own with c in the picker's profile screen (p)",
		fsutil.ShortenHome(a.opt.file), profile.Default, profile.Default)
}

// profileInfo is one profile as `profile list` prints it.
type profileInfo struct {
	Name    string   `json:"name"`
	Servers []string `json:"servers"`
	Source  string   `json:"source"`
	Missing []string `json:"missing,omitempty"`
	// Shadowed marks a catalog profile a personal one of the same name hides.
	Shadowed bool `json:"shadowed,omitempty"`
	// Conflict marks a catalog profile named like the built-in default,
	// which --profile refuses until it is renamed.
	Conflict bool `json:"conflict,omitempty"`
}

func (a *app) has(name string) bool {
	_, ok := a.cat.Find(name)
	return ok
}

// defaultServers is what the built-in default profile — and so --all —
// selects, in catalog order: every server but the ones disabled in Claude
// Code (when skipDisabled, i.e. the agent reads Claude's settings; turning
// one back on is an explicit, per-server choice) and the ones hidden in the
// picker with h (hidden means not wanted in this project). The one rule, so
// `profile list` and the `list` footer describe what --all launches.
func (a *app) defaultServers(skipDisabled bool) ([]string, error) {
	hidden, err := state.LoadHidden(a.root)
	if err != nil {
		return nil, err
	}
	return a.eligible(skipDisabled, hidden), nil
}

// eligible applies defaultServers' rule to a given hidden set.
func (a *app) eligible(skipDisabled bool, hidden map[string]bool) []string {
	out := []string{}
	for _, s := range a.cat.Servers {
		if (!skipDisabled || !s.Disabled) && !hidden[s.Name] {
			out = append(out, s.Name)
		}
	}
	return out
}

// profileRows lists every profile in the order the picker shows them: the
// user's own, the catalog's, then default — everything --all would select.
func (a *app) profileRows() []profileInfo {
	var rows []profileInfo
	for _, p := range a.profiles().Profiles {
		rows = append(rows, profileInfo{Name: p.Name, Servers: p.Servers, Source: sourcePersonal,
			Missing: profile.Missing(p.Servers, a.has)})
	}
	for _, n := range a.cat.ProfileNames() {
		_, _, shadowed := a.profiles().Find(n)
		rows = append(rows, profileInfo{Name: n, Servers: a.cat.Profiles[n], Source: sourceCatalog,
			Missing: profile.Missing(a.cat.Profiles[n], a.has), Shadowed: shadowed, Conflict: n == profile.Default})
	}
	all, err := a.defaultServers(true)
	if err != nil {
		// The hidden set could not be read: say so and describe default
		// without it, rather than fail a listing over a state file.
		a.warnf("warning: %s could not be read (%v); default is described without the hidden set",
			fsutil.ShortenHome(state.HiddenPath(a.root)), err)
		all = a.eligible(true, nil)
	}
	return append(rows, profileInfo{Name: profile.Default, Servers: all, Source: sourceBuiltin})
}

// profileSelection resolves --profile NAME: the user's own first, then the
// catalog's. Servers this project lacks are skipped with one warning.
// personal says the profile is the user's own: a list of names the user
// wrote, which is consent to measure what it names; a catalog profile is
// the repository's list, which is not (see app.consent).
func (a *app) profileSelection(name string) (sel map[string]bool, personal bool, err error) {
	var servers []string
	if name == profile.Default {
		// run() has already turned the built-in one into --all; only the
		// catalog collision gets here.
		return nil, false, a.defaultConflict()
	}
	if err := a.profiles().Err(); err != nil {
		// A personal profile of this name could be hidden behind the error,
		// and it would shadow the catalog's; falling back to the catalog's
		// could run different servers than the user asked for.
		return nil, false, fmt.Errorf("--profile %s: %s could not be read (%v); fix or remove the file first, "+
			"since a personal profile of that name would take precedence", name,
			fsutil.ShortenHome(a.profiles().Path()), err)
	}
	if p, _, ok := a.profiles().Find(name); ok {
		servers, personal = p.Servers, true
		if missing := profile.Missing(servers, a.has); len(missing) > 0 {
			a.warnf("warning: profile %s lists %s, not in this project's catalog; skipped",
				name, strings.Join(missing, ", "))
		}
	} else if s, ok := a.cat.Profiles[name]; ok {
		servers = s // catalog.Load has already warned about missing servers
	} else {
		return nil, false, fmt.Errorf("no profile %q (%s)", name, a.profileSources())
	}
	sel = map[string]bool{}
	for _, n := range servers {
		if a.has(n) {
			sel[n] = true
		}
	}
	return sel, personal, nil
}

func (a *app) profileSources() string {
	yours := strings.Join(a.profiles().Names(), ", ")
	if yours == "" {
		yours = "none"
	}
	cat := strings.Join(a.cat.ProfileNames(), ", ")
	if cat == "" {
		cat = "none"
	}
	return fmt.Sprintf("yours: %s; in %s: %s; built-in: %s",
		yours, fsutil.ShortenHome(a.opt.file), cat, profile.Default)
}

func (a *app) profileCmd(rest []string) error {
	sub := "list"
	if len(rest) > 0 {
		sub, rest = rest[0], rest[1:]
	}
	set := a.profiles()
	where := fsutil.ShortenHome(set.Path())
	switch sub {
	case "list":
		rows := a.profileRows()
		if a.opt.jsonOut {
			return a.printJSON(rows)
		}
		// Names come from the catalog and the profiles file: safe to
		// print (trust.Safe) before they reach the terminal.
		w := 0
		for _, r := range rows {
			w = max(w, len(trust.Safe(r.Name)))
		}
		for _, r := range rows {
			source := r.Source
			if r.Shadowed {
				source += ", shadowed"
			}
			if r.Conflict {
				source += ", rename it"
			}
			fmt.Fprintf(a.out, "%-*s  %-18s  %s\n", w, trust.Safe(r.Name), source, trust.Safe(describeServers(r)))
		}
		return nil
	case "save":
		if len(rest) != 1 {
			return fmt.Errorf("profile save needs a name")
		}
		sel, err := a.selection(nil)
		if err != nil {
			return err
		}
		var names []string
		for _, s := range a.cat.Servers {
			if sel[s.Name] {
				names = append(names, s.Name)
			}
		}
		if len(names) == 0 {
			return fmt.Errorf("nothing to save: the selection is empty (use --select a,b)")
		}
		if err := set.Update(func(s *profile.Set) error { return s.Put(rest[0], names) }); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "profile %s = %s (%s)\n", trust.Safe(rest[0]), trust.Safe(strings.Join(names, ", ")), where)
		return nil
	case "delete", "rm":
		if len(rest) != 1 {
			return fmt.Errorf("profile delete needs a name")
		}
		// Only in the catalog: delete it from the file it came from. The
		// name was typed out, which is the confirmation.
		if _, _, personal := set.Find(rest[0]); !personal && rest[0] != profile.Default {
			if file, ok := a.cat.ProfileFiles[rest[0]]; ok {
				if err := catalog.DeleteProfile(file, rest[0]); err != nil {
					return err
				}
				fmt.Fprintf(a.out, "deleted profile %s (%s)\n", trust.Safe(rest[0]), fsutil.ShortenHome(file))
				return nil
			}
		}
		if err := a.refuseReadOnly(rest[0], "removed"); err != nil {
			return err
		}
		if err := set.Update(func(s *profile.Set) error { return s.Delete(rest[0]) }); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "deleted profile %s (%s)\n", trust.Safe(rest[0]), where)
		return nil
	case "rename":
		if len(rest) != 2 {
			return fmt.Errorf("profile rename needs the old and the new name")
		}
		if err := a.refuseReadOnly(rest[0], "renamed"); err != nil {
			return err
		}
		if err := set.Update(func(s *profile.Set) error { return s.Rename(rest[0], rest[1]) }); err != nil {
			return err
		}
		fmt.Fprintf(a.out, "renamed profile %s to %s (%s)\n", trust.Safe(rest[0]), trust.Safe(rest[1]), where)
		return nil
	}
	return fmt.Errorf("unknown profile command %q (list, save, delete, rename)", sub)
}

// refuseReadOnly explains why default and the catalog's profiles cannot be
// edited from here, so the error names the file to edit instead.
func (a *app) refuseReadOnly(name, verb string) error {
	if name == profile.Default {
		return fmt.Errorf("%s is built-in and cannot be %s", profile.Default, verb)
	}
	if _, _, ok := a.profiles().Find(name); ok {
		return nil
	}
	if _, ok := a.cat.Profiles[name]; ok {
		return fmt.Errorf("%q is defined in %s; %s it there", name, fsutil.ShortenHome(a.opt.file),
			map[string]string{"removed": "remove", "renamed": "rename"}[verb])
	}
	return nil
}

// describeServers lists a profile's servers, marking the ones missing here.
func describeServers(r profileInfo) string {
	missing := map[string]bool{}
	for _, n := range r.Missing {
		missing[n] = true
	}
	parts := make([]string, 0, len(r.Servers))
	for _, n := range r.Servers {
		if missing[n] {
			n += " (missing)"
		}
		parts = append(parts, n)
	}
	return strings.Join(parts, ", ")
}
