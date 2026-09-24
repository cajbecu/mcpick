package spec

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/cajbecu/mcpick/internal/fsutil"
)

// envRef matches ${VAR}, ${VAR:-default} and ${VAR:?why}, optionally escaped by
// doubling the dollar sign.
var envRef = regexp.MustCompile(`(\$?)\$\{([A-Za-z_][A-Za-z0-9_]*)(?::([-?])([^}]*))?\}`)

type expandError struct {
	Var    string
	Reason string
}

func (e *expandError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("%s is required: %s", e.Var, e.Reason)
	}
	return fmt.Sprintf("%s is required but unset", e.Var)
}

// Expand replaces {UUID} with the session id and ${VAR} forms with the
// environment, recursively across every string in the spec. Only the rendered
// config is touched; the catalog on disk keeps its placeholders.
//
//	${VAR}           the value, empty when unset
//	${VAR:-default}  default when unset or empty
//	${VAR:?why}      an error when unset or empty
//	$${VAR}          the literal text ${VAR}
func Expand(v any, uid string) (any, error) {
	switch t := v.(type) {
	case string:
		s := strings.ReplaceAll(t, "{UUID}", uid)
		var firstErr error
		out := envRef.ReplaceAllStringFunc(s, func(m string) string {
			g := envRef.FindStringSubmatch(m)
			escape, name, op, arg := g[1], g[2], g[3], g[4]
			if escape != "" {
				return m[1:] // $${VAR} -> ${VAR}
			}
			val, ok := os.LookupEnv(name)
			switch op {
			case "?":
				if !ok || val == "" {
					if firstErr == nil {
						firstErr = &expandError{Var: name, Reason: arg}
					}
					return ""
				}
			case "-":
				if !ok || val == "" {
					return arg
				}
			}
			return val
		})
		return out, firstErr
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			e, err := Expand(val, uid)
			if err != nil {
				return nil, err
			}
			out[k] = e
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			e, err := Expand(val, uid)
			if err != nil {
				return nil, err
			}
			out[i] = e
		}
		return out, nil
	}
	return v, nil
}

// Selection is the resolved set of servers a session will run with, in catalog
// order and already expanded.
type Selection struct {
	Names []string
	Specs map[string]map[string]any
}

func (s Selection) Empty() bool { return len(s.Names) == 0 }

// secretish reports the spec keys whose values should never be written to a
// file that might be committed.
func secretish(key string) bool {
	k := strings.ToLower(key)
	for _, needle := range []string{"token", "secret", "key", "password", "authorization", "auth"} {
		if strings.Contains(k, needle) {
			return true
		}
	}
	return false
}

// Redact rewrites literal secrets into ${VAR} references and reports the
// variables the user now has to export. Used by import so seeding a catalog
// from ~/.claude.json does not move live credentials into the repo.
func Redact(name string, spec map[string]any) (map[string]any, []string) {
	var vars []string
	varName := func(field string) string {
		s := strings.ToUpper(fsutil.Sanitize(name) + "_" + fsutil.Sanitize(field))
		return strings.ReplaceAll(s, "-", "_")
	}
	var walk func(any, string) any
	walk = func(v any, field string) any {
		switch t := v.(type) {
		case string:
			if !secretish(field) || t == "" || strings.Contains(t, "${") {
				return t
			}
			vn := varName(field)
			// Bearer <token> keeps its scheme in the catalog so the header
			// stays readable; only the credential itself moves to the variable.
			if scheme, rest, ok := strings.Cut(t, " "); ok && rest != "" &&
				(strings.EqualFold(scheme, "bearer") || strings.EqualFold(scheme, "basic")) {
				vars = append(vars, vn+"="+rest)
				return scheme + " ${" + vn + "}"
			}
			vars = append(vars, vn+"="+t)
			return "${" + vn + "}"
		case map[string]any:
			out := make(map[string]any, len(t))
			for k, val := range t {
				sub := k
				if secretish(field) {
					sub = field + "_" + k
				}
				out[k] = walk(val, sub)
			}
			return out
		case []any:
			out := make([]any, len(t))
			for i, val := range t {
				out[i] = walk(val, field)
			}
			return out
		}
		return v
	}
	out := map[string]any{}
	for k, v := range spec {
		out[k] = walk(v, k)
	}
	sort.Strings(vars)
	return out, vars
}
