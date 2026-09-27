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
	// Raw are the specs as the catalog wrote them, placeholders intact,
	// for a backend that has to know what was expanded — or may leave a
	// reference for the agent to expand. Nil when the caller built the
	// selection from expanded specs itself.
	Raw map[string]map[string]any
}

func (s Selection) Empty() bool { return len(s.Names) == 0 }

// secretWords mark a key, flag or URL parameter name as carrying a
// credential, one word of the name at a time (API_KEY, apiKey, --api-key,
// x-api-key all split into api and key). Loose on purpose: a value that
// was not secret costs one export; a secret that reaches git costs more.
var secretWords = map[string]bool{
	"key": true, "apikey": true, "token": true, "secret": true, "password": true,
	"passwd": true, "pwd": true, "passphrase": true, "pat": true, "auth": true,
	"authorization": true, "credential": true, "credentials": true, "bearer": true,
	"cookie": true,
}

// plainWords name something about a credential rather than the credential:
// its file, its id, the header it goes in, or the flag's negation.
var plainWords = map[string]bool{
	"file": true, "path": true, "dir": true, "id": true, "name": true, "url": true,
	"type": true, "mode": true, "header": true, "method": true, "scheme": true,
	"hint": true, "no": true, "skip": true, "disable": true, "without": true,
}

// camelBoundary is where a camelCase name splits: a lower-case letter or a
// digit followed by an upper-case one.
var camelBoundary = regexp.MustCompile(`([a-z0-9])([A-Z])`)

// nameWords splits a key, flag or parameter name into its lower-case words:
// on anything that is not a letter or digit, and on camelCase boundaries.
func nameWords(name string) []string {
	name = camelBoundary.ReplaceAllString(name, "$1 $2")
	return strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
}

// secretish reports whether a key, flag or URL parameter name says its
// value is a credential: one of its words is a secret word and none is a
// plain one, so API_KEY, GITHUB_TOKEN, Authorization, --api-key, ?token=
// and DB_PASSWORD are, and KEY_FILE, CLIENT_ID, --no-auth, TOKEN_URL and
// keyword are not.
func secretish(key string) bool {
	found := false
	for _, w := range nameWords(key) {
		if plainWords[w] {
			return false
		}
		if secretWords[w] || secretSuffix(w) {
			found = true
		}
	}
	return found
}

// secretSuffix catches a word that runs a secret word together with a
// prefix — PGPASSWORD, accesstoken, clientsecret — for the words long
// enough that no ordinary word ends the same way (monkey, keyword).
func secretSuffix(w string) bool {
	for _, suffix := range []string{"password", "passwd", "token", "secret"} {
		if strings.HasSuffix(w, suffix) {
			return true
		}
	}
	return false
}

// knownToken matches a credential by its well-known prefix, wherever it is
// written: GitHub (ghp_, gho_, github_pat_), OpenAI-style (sk-), Stripe
// (sk_live_), Slack (xoxb-, xoxp-), AWS (AKIA) and GitLab (glpat-). A match
// still has to pass highEntropy.
var knownToken = regexp.MustCompile(`\b(?:ghp_[A-Za-z0-9]{20,}|gho_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|sk_live_[A-Za-z0-9]{16,}|sk-[A-Za-z0-9_-]{16,}|xox[bp]-[A-Za-z0-9-]{16,}|AKIA[A-Z0-9]{16}|glpat-[A-Za-z0-9_-]{16,})\b`)

// highEntropy is a cheap stand-in for entropy: a real token draws on many
// characters, a placeholder like sk-xxxxxxxxxxxxxxxx does not.
func highEntropy(s string) bool {
	seen := map[rune]bool{}
	for _, r := range s {
		seen[r] = true
	}
	return len(seen) >= 8
}

// urlPassword matches scheme://user:password@ anywhere in a value: a
// database URL in env, a URL in args, the server's own url.
var urlPassword = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://[^/?#@:\s]*):([^/?#@\s]+)@`)

// urlParam matches one ?name=value or &name=value; whether it is a secret
// depends on the name.
// urlParam matches a query parameter, or one in the fragment: OAuth's
// implicit flow puts #access_token=… there.
var urlParam = regexp.MustCompile(`([?&#])([^=&#\s]+)=([^&#\s]+)`)

// flagValue is what an argument after a credential flag must look like to
// be taken as its value: one word that is not a flag, not a boolean, not a
// placeholder. `--token --verbose`, `--auth false` and `--key ${K}` are
// left alone.
func flagValue(a string) bool {
	if a == "" || strings.HasPrefix(a, "-") || strings.Contains(a, "${") || strings.ContainsAny(a, " \t\n") {
		return false
	}
	switch strings.ToLower(a) {
	case "true", "false", "yes", "no", "on", "off", "0", "1":
		return false
	}
	return true
}

// credScheme matches a value that is an HTTP credential by its form, whatever
// the key it is under: `Bearer <token>`, `Basic <token>`, `Token <token>`,
// with at least 8 characters of credential. Group 1 is the scheme and the
// space after it, which stay in the catalog so the value remains readable;
// group 2 is the credential.
var credScheme = regexp.MustCompile(`(?is)^((?:bearer|basic|token)\s+)(\S{8,}.*)$`)

// headerFlag is a curl-style header flag, whose value is `Name: value`.
func headerFlag(a string) bool { return a == "-H" || a == "--header" }

// hasRef says whether a value holds a `${VAR}` reference (or its escaped
// `$${VAR}` form). A match that holds one is left as written: it is the
// user's variable already, or text around it that cannot be moved to a
// variable of its own without losing the reference.
func hasRef(s string) bool { return strings.Contains(s, "${") }

// envAssign matches K=V with K an environment-variable name.
var envAssign = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)=(.+)$`)

// Placeholder is the reference Redact writes for a variable: `${NAME:?export
// NAME}`, so a launch with the variable unset fails saying what to export,
// instead of sending an empty credential.
func Placeholder(name string) string { return "${" + name + ":?export " + name + "}" }

// Redact rewrites the credentials written in a spec into ${VAR} references
// (Placeholder) and reports the variables the user now has to export, as
// NAME=value lines, sorted. It is what keeps import and move-to-project
// from putting a live credential into a file that sits in git. Detected:
//
//   - a value under a secret-sounding key (secretish): a header, an env
//     variable, any spec key;
//   - the value of a credential flag in args, `--api-key VALUE` or
//     `--api-key=VALUE`, and a K=V argument with a secret-sounding K;
//   - the password of a scheme://user:password@ URL anywhere, env values
//     (DATABASE_URL) included;
//   - the value of a URL query parameter with a secret-sounding name;
//   - a token with a well-known prefix (knownToken) anywhere;
//   - a value of the form `Bearer|Basic|Token <credential>` (credScheme)
//     under any key — AUTH_HEADER, X-Custom — in env, headers and args;
//   - the value of a header given as `-H 'Name: value'` or `--header`
//     in args, by the header's name or by its form.
//
// Names are derived from the server and the field (GITHUB_API_KEY); the
// same value gets the same name wherever it appears, and two values that
// would share a name are told apart by a suffix. What is already a
// `${VAR}` reference is left as written, and so is a match that holds
// one; the rest of the value is still searched (`?user=${USER}&token=abc`
// redacts the token).
func Redact(name string, spec map[string]any) (map[string]any, []string) {
	r := &redactor{server: name, byValue: map[string]string{}, taken: map[string]bool{}}
	out := map[string]any{}
	// In key order, so the names are the same on every run: which value
	// gets a name first, and which the suffix, must not depend on a map.
	for _, k := range SortedKeys(spec) {
		v := spec[k]
		if k == "args" {
			if list, ok := v.([]any); ok {
				out[k] = r.args(list)
				continue
			}
		}
		out[k] = r.walk(v, k)
	}
	vars := make([]string, 0, len(r.byValue))
	for val, vn := range r.byValue {
		vars = append(vars, vn+"="+val)
	}
	sort.Strings(vars)
	return out, vars
}

type redactor struct {
	server  string
	byValue map[string]string // secret value -> variable name, so a repeat reuses it
	taken   map[string]bool   // variable names given out
}

// varName derives the variable for a field: SERVER_FIELD, upper-case, with
// anything that is not a letter or digit as an underscore.
func (r *redactor) varName(field string) string {
	s := strings.ToUpper(fsutil.Sanitize(r.server) + "_" + fsutil.Sanitize(strings.TrimLeft(field, "-")))
	return strings.Trim(strings.ReplaceAll(s, "-", "_"), "_")
}

// ref records value as the credential behind the variable derived from
// field and returns its placeholder. The same value keeps its first name;
// a different value that lands on a taken name gets _2, _3, ...
func (r *redactor) ref(field, value string) string {
	if vn, ok := r.byValue[value]; ok {
		return Placeholder(vn)
	}
	base := r.varName(field)
	vn := base
	for n := 2; r.taken[vn]; n++ {
		vn = fmt.Sprintf("%s_%d", base, n)
	}
	r.taken[vn] = true
	r.byValue[value] = vn
	return Placeholder(vn)
}

func (r *redactor) walk(v any, field string) any {
	switch t := v.(type) {
	case string:
		return r.text(t, field)
	case map[string]any:
		out := make(map[string]any, len(t))
		for _, k := range SortedKeys(t) {
			sub := k
			if secretish(field) {
				sub = field + "_" + k
			}
			out[k] = r.walk(t[k], sub)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = r.walk(val, field)
		}
		return out
	}
	return v
}

// text redacts one string value in the field named.
func (r *redactor) text(t, field string) string {
	if t == "" {
		return t
	}
	// Bearer <token> keeps its scheme in the catalog so the header stays
	// readable; only the credential itself moves to the variable.
	if g := credScheme.FindStringSubmatch(t); g != nil && !hasRef(g[2]) {
		return g[1] + r.ref(field, g[2])
	}
	if secretish(field) {
		if hasRef(t) {
			return t
		}
		if scheme, rest, ok := strings.Cut(t, " "); ok && rest != "" &&
			(strings.EqualFold(scheme, "bearer") || strings.EqualFold(scheme, "basic") || strings.EqualFold(scheme, "token")) {
			return scheme + " " + r.ref(field, rest)
		}
		return r.ref(field, t)
	}
	return r.patterns(t, field)
}

// header redacts a `Name: value` header given in args: by the header's
// name, as for a key in `headers`, or by the value's form. Anything else
// is searched as any argument is.
func (r *redactor) header(a string) string {
	name, val, ok := strings.Cut(a, ":")
	if !ok || strings.TrimSpace(name) == "" || strings.ContainsAny(name, " \t") {
		return r.patterns(a, "args")
	}
	trimmed := strings.TrimLeft(val, " \t")
	lead := val[:len(val)-len(trimmed)]
	if trimmed == "" {
		return a
	}
	return name + ":" + lead + r.text(trimmed, name)
}

// patterns redacts what a value carries inside it, whatever its key: a URL
// password, a secret-sounding query parameter, a known token. A match that
// holds a `${VAR}` is left alone; the rest of the value is not.
func (r *redactor) patterns(t, field string) string {
	t = urlPassword.ReplaceAllStringFunc(t, func(m string) string {
		g := urlPassword.FindStringSubmatch(m)
		if hasRef(g[2]) {
			return m
		}
		return g[1] + ":" + r.ref(passwordField(field), g[2]) + "@"
	})
	t = urlParam.ReplaceAllStringFunc(t, func(m string) string {
		g := urlParam.FindStringSubmatch(m)
		if !secretish(g[2]) || hasRef(g[3]) {
			return m
		}
		return g[1] + g[2] + "=" + r.ref(g[2], g[3])
	})
	return knownToken.ReplaceAllStringFunc(t, func(m string) string {
		if !highEntropy(m) {
			return m
		}
		return r.ref(field, m)
	})
}

// passwordField names the variable for a URL password: PASSWORD for the
// server's own url, FIELD_PASSWORD elsewhere (DATABASE_URL_PASSWORD).
func passwordField(field string) string {
	if field == "url" {
		return "password"
	}
	return field + "_password"
}

// args redacts the argument list: the value after a credential flag, a
// `--flag=value`, a K=V with a secret-sounding K, a header after -H or
// --header, a `Bearer <token>` argument, and then whatever each argument
// carries inside it.
func (r *redactor) args(list []any) []any {
	out := make([]any, len(list))
	flag := ""      // the credential flag whose value is the next argument
	header := false // the previous argument was -H or --header
	for i, v := range list {
		a, ok := v.(string)
		if !ok {
			out[i] = r.walk(v, "args")
			flag, header = "", false
			continue
		}
		if header {
			header = false
			out[i] = r.header(a)
			continue
		}
		switch {
		case flag != "" && flagValue(a):
			out[i] = r.ref(flag, a)
			flag = ""
			continue
		case headerFlag(a):
			flag, header = "", true
			out[i] = a
			continue
		case strings.HasPrefix(a, "--header="):
			flag = ""
			out[i] = "--header=" + r.header(strings.TrimPrefix(a, "--header="))
			continue
		case strings.HasPrefix(a, "-"):
			flag = ""
			name, val, has := strings.Cut(a, "=")
			if !secretish(name) {
				out[i] = r.patterns(a, "args")
				continue
			}
			if !has {
				out[i], flag = a, name
				continue
			}
			if flagValue(val) {
				out[i] = name + "=" + r.ref(name, val)
				continue
			}
		default:
			flag = ""
			if g := envAssign.FindStringSubmatch(a); g != nil && secretish(g[1]) && flagValue(g[2]) {
				out[i] = g[1] + "=" + r.ref(g[1], g[2])
				continue
			}
			if g := credScheme.FindStringSubmatch(a); g != nil && !hasRef(g[2]) {
				out[i] = g[1] + r.ref("args", g[2])
				continue
			}
		}
		out[i] = r.patterns(a, "args")
	}
	return out
}

// bearerText matches a bearer credential in free text: `Bearer <token>`.
var bearerText = regexp.MustCompile(`(?i)\b(bearer\s+)[A-Za-z0-9._~+/=-]{8,}`)

// MaskText hides in free text what Redact would find inside a value: a URL
// password, a secret-sounding query parameter's value, a known token and a
// `Bearer <token>` become ***. For text that is kept — an error message
// written to a state file — rather than rewritten.
func MaskText(t string) string {
	t = urlPassword.ReplaceAllString(t, "${1}:***@")
	t = bearerText.ReplaceAllString(t, "${1}***")
	t = urlParam.ReplaceAllStringFunc(t, func(m string) string {
		g := urlParam.FindStringSubmatch(m)
		if !secretish(g[2]) || strings.HasPrefix(g[3], "${") {
			return m
		}
		return g[1] + g[2] + "=***"
	})
	return knownToken.ReplaceAllStringFunc(t, func(m string) string {
		if !highEntropy(m) {
			return m
		}
		return "***"
	})
}
