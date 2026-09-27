package cli

import (
	"strings"

	"github.com/cajbecu/mcpick/internal/catalog"
	"github.com/cajbecu/mcpick/internal/fsutil"
	"github.com/cajbecu/mcpick/internal/mcp"
	"github.com/cajbecu/mcpick/internal/spec"
	"github.com/cajbecu/mcpick/internal/trust"
)

// gated is what a measurement may run and what it leaves out, decided by
// trust.Gate the same way the picker decides: a stdio server's command runs
// only once trusted; a remote server from the repository's catalog once
// checked (app.consent: --select, a personal profile, or a saved check
// that still covers it — not --all or a catalog profile), or once the
// catalog is trusted, or — neither — when its spec exposes nothing, and
// then onto a public address only. --trust is the
// command line's consent for commands: the untrusted ones are granted,
// remembered for this project, and run. --trust-catalog is the consent for
// the catalog: recorded for this project, keyed on the files' contents.
type gated struct {
	run map[string]bool
	// opts is the connection option a server runs with: PublicOnly for a
	// repository remote measured on the strength of its spec alone.
	opts map[string]mcp.Options
	// skipped is one result per server left out, so --json shows the whole
	// picture; they are never cached, because nothing was measured.
	skipped   []mcp.Result
	untrusted []string // "name (command)" for the warning
	unchecked []string // "name (what measuring it would expose)"
	changed   bool     // the catalog trust lapsed: a file changed
}

func (a *app) gate(servers []catalog.Server, checked map[string]bool) gated {
	store := trust.Load()
	g := gated{run: map[string]bool{}, opts: map[string]mcp.Options{}}
	sc := trust.Scope{Store: store, Root: a.root}
	// The hash is over the bytes the catalog was parsed from, so what
	// --trust-catalog records is what it lists, whatever the file holds now.
	hash, files := trust.CatalogHash(a.cat.Files)
	sc.Catalog = store.Catalog(a.root, hash)
	if a.opt.trustCatalog && sc.Catalog != trust.CatalogTrusted {
		sc.Catalog = a.trustCatalog(store, hash, files)
	}
	var granted []string
	for _, s := range servers {
		v := trust.Gate(sc, s, checked[s.Name])
		if !v.Run && v.NeedsTrust && a.opt.trust {
			// --trust approves what the catalog says without a prompt, so
			// say what that is before it runs: the command and its
			// arguments, with what looks like a credential masked in
			// arguments and env alike (it is stderr, which ends up in
			// logs, not a consent screen).
			a.warnf("--trust approves %s: %s", trust.Safe(s.Name), spec.MaskText(trust.Describe(s.Spec, trust.MaskAll)))
			store.Grant(a.root, s.Name, s.Spec)
			granted = append(granted, trust.Safe(s.Name))
			v = trust.Verdict{Run: true}
		}
		if v.Run {
			g.run[s.Name] = true
			if v.PublicOnly {
				g.opts[s.Name] = mcp.Options{PublicOnly: true}
			}
			continue
		}
		// The reason and the warning carry trust.Describe's masked text,
		// not the raw command line: nobody consented on this output, so a
		// credential written in args or env is masked and a terminal
		// escape is spelled out, on stderr and in --json alike.
		r := mcp.Result{Name: s.Name, Skipped: true, Remote: spec.ViewOf(s.Spec).Remote()}
		if v.NeedsTrust {
			r.Err = "skipped: " + v.Reason + " (untrusted; --trust to run it)"
			g.untrusted = append(g.untrusted, trust.Safe(s.Name)+" ("+trust.Describe(s.Spec, trust.MaskAll)+")")
		} else {
			r.Err = "skipped: " + v.Reason + " (--select it to measure, or --trust-catalog)"
			if v.CatalogChanged {
				r.Err += "; catalog changed since you trusted it"
				g.changed = true
			}
			g.unchecked = append(g.unchecked, trust.Safe(s.Name)+" ("+v.Exposure+")")
		}
		g.skipped = append(g.skipped, r)
	}
	if len(granted) > 0 {
		// A store that cannot be written is reported, and the commands run
		// anyway: the consent was given on this command line.
		if err := store.Save(); err != nil {
			a.warnf("could not save trust: %v (the commands run anyway)", err)
		} else {
			a.warnf("trusted %d command(s) for %s: %s", len(granted), a.root, strings.Join(granted, ", "))
		}
	}
	return g
}

// trustCatalog is --trust-catalog: it records the trust in the project's
// catalog without a prompt, so it first says what that is — the files, and
// each remote server the trust lets a measurement contact, with its URL and
// header names — the way --trust prints each command it approves. It
// returns the status the gate runs with.
func (a *app) trustCatalog(store *trust.Store, hash string, files []string) trust.CatalogStatus {
	if hash == "" {
		a.warnf("--trust-catalog: no catalog file in %s; nothing to trust", a.root)
		return trust.CatalogUntrusted
	}
	var shown []string
	for _, f := range files {
		shown = append(shown, fsutil.ShortenHome(f))
	}
	a.warnf("--trust-catalog trusts %s: its remote servers are measured with your environment expanded into URL and headers",
		strings.Join(shown, ", "))
	for _, s := range a.cat.Servers {
		if s.Origin == catalog.OriginProject && spec.ViewOf(s.Spec).Remote() {
			a.warnf("  %s: %s", trust.Safe(s.Name), trust.DescribeRemote(s.Spec))
		}
	}
	store.TrustCatalog(a.root, hash, files)
	if err := store.Save(); err != nil {
		a.warnf("could not save trust: %v (this run measures the catalog's servers anyway)", err)
	} else {
		a.warnf("trusted this repository's catalog for %s (stdio commands still need --trust)", a.root)
	}
	return trust.CatalogTrusted
}

// warn says what was left out and what would lift it.
func (g gated) warn(a *app) {
	if len(g.untrusted) > 0 {
		a.warnf("not measured, their commands are not trusted: %s; re-run with --trust to run them and remember that",
			strings.Join(g.untrusted, ", "))
	}
	if len(g.unchecked) > 0 {
		why := "name them with --select or tick them in the picker, or --trust-catalog to trust this repository's catalog; --all and a catalog profile are not a check"
		if g.changed {
			why = "the catalog changed since you trusted it; --trust-catalog to trust it again, or --select them"
		}
		a.warnf("not measured, defined by this repository and not checked: %s (%s)", strings.Join(g.unchecked, ", "), why)
	}
}
