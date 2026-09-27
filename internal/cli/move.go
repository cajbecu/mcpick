package cli

// `mcpick move <server> <project|local|user>`: the picker's v from the
// command line, with the same rules (catalog.Move). The one question — a
// credential written in the spec, about to land in the project catalog —
// is answered by --yes or --redact; on a terminal it is asked, and without
// one it is refused, since a script must not put a token in git by default.

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/cajbecu/mcpick/internal/catalog"
	"github.com/cajbecu/mcpick/internal/fsutil"
	"github.com/cajbecu/mcpick/internal/spec"
	"github.com/cajbecu/mcpick/internal/trust"
)

func (a *app) move(rest []string) error {
	if len(rest) != 2 {
		return fmt.Errorf("move needs a server and a group: mcpick move <server> <%s>", strings.Join(catalog.Writable, "|"))
	}
	name, to := rest[0], rest[1]
	if err := a.cat.CanMove(name, to); err != nil {
		return err
	}
	s, _ := a.cat.Find(name)
	sp := s.Spec
	var vars []string
	if to == catalog.OriginProject && s.Origin != catalog.OriginProject {
		if redacted, found := spec.Redact(name, s.Spec); len(found) > 0 {
			redact, err := a.confirmSecrets(name)
			if err != nil {
				return err
			}
			if redact {
				sp, vars = redacted, found
			}
		}
	}
	m, err := a.cat.Move(name, to, sp)
	if err != nil {
		return err
	}
	// The account names the server and its files, the catalog's text:
	// safe to print before it reaches the terminal (warnf does its own).
	fmt.Fprintln(a.out, trust.Safe(m.Describe()))
	for _, n := range m.Notes() {
		a.warnf("%s", n)
	}
	if len(vars) > 0 {
		fmt.Fprintln(a.out, "\nexport these before launching:")
		for _, v := range vars {
			name, _, _ := strings.Cut(v, "=")
			fmt.Fprintf(a.out, "  export %s=...\n", trust.Safe(name))
		}
		a.warnf("the values were left out of the catalog on purpose; they are now only in the backup of %s (%s.mcpick-bak-*)",
			fsutil.ShortenHome(m.FromFile), fsutil.ShortenHome(m.FromFile))
	}
	return nil
}

// confirmSecrets decides what happens to a credential written in the spec
// of name, about to be written into the project catalog: --redact writes
// a ${VAR} reference, --yes writes the value, a terminal is asked, and
// anything else is refused.
func (a *app) confirmSecrets(name string) (redact bool, err error) {
	file := fsutil.ShortenHome(a.opt.file)
	switch {
	case a.opt.redact:
		return true, nil
	case a.opt.yes:
		return false, nil
	case !interactive():
		return false, fmt.Errorf("%s holds credentials as written, which would be written into %s; re-run with --yes to write them as they are, or --redact to write ${VAR} references instead", name, file)
	}
	fmt.Fprintf(a.errw, "%s holds credentials as written; they will be written into %s.\n"+
		"type 'yes' to write them as they are, r to write ${VAR} references instead, anything else cancels: ", name, file)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	switch strings.TrimSpace(line) {
	case "yes":
		return false, nil
	case "r":
		return true, nil
	}
	return false, fmt.Errorf("cancelled")
}
