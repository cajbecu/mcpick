package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// escapes is what a hostile server can put in text that reaches the
// terminal: OSC 52 (writes the clipboard), ESC[2J (clears the screen), the
// same CSI as one C1 byte in UTF-8, and a right-to-left override.
const escapes = "\x1b]52;c;aGk=\x07 \x1b[2J \u009b2J \u202e"

// unsafe reports whether s still carries one of the escapes raw.
func unsafe(s string) bool {
	return strings.ContainsAny(s, "\x1b\u009b\u202e\x07")
}

// An HTTP error body is the server's own text and ends up in doctor's
// output and the picker's status line: its escapes are spelled out where
// the error is built, so every printer is covered at once.
func TestHTTPErrorBodyIsSafeToPrint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom "+escapes, http.StatusInternalServerError)
	}))
	defer srv.Close()
	res := Probe(context.Background(), "hostile", map[string]any{"type": "http", "url": srv.URL}, 5*time.Second)
	if res.OK {
		t.Fatal("a 500 is not a healthy server")
	}
	if unsafe(res.Err) {
		t.Fatalf("error text carries a raw terminal escape: %q", res.Err)
	}
	for _, want := range []string{"HTTP 500", "boom", `\x1b]52`, `\x1b[2J`, `\u009b`, `\u202e`} {
		if !strings.Contains(res.Err, want) {
			t.Errorf("error %q lacks %q", res.Err, want)
		}
	}
}

// A stdio server's last words on stderr become the error when it exits;
// they are spelled out the same way.
func TestStdioStderrTailIsSafeToPrint(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := Dial(ctx, stdioView(t))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	err = conn.Call(ctx, "crash", nil, nil)
	if err == nil {
		t.Fatal("the crash must fail the call")
	}
	if unsafe(err.Error()) {
		t.Fatalf("stderr tail carries a raw terminal escape: %q", err)
	}
	if !strings.Contains(err.Error(), `\x1b[2J`) || !strings.Contains(err.Error(), `\u009b`) {
		t.Errorf("the escape should be spelled out: %q", err)
	}
}

// A transport error carries the URL the request went to, expanded: the
// query values and the userinfo password in it are redacted before the
// error leaves the package, whichever transport failed.
func TestURLErrorRedactsQueryValues(t *testing.T) {
	for _, transport := range []string{"http", "sse"} {
		sp := map[string]any{"type": transport, "url": "http://127.0.0.1:1/mcp?api_key=sekret-value&v=2"}
		res := Probe(context.Background(), transport, sp, 2*time.Second)
		if res.OK {
			t.Fatalf("%s: a closed port is not a healthy server", transport)
		}
		if strings.Contains(res.Err, "sekret-value") {
			t.Errorf("%s: the query value leaked: %q", transport, res.Err)
		}
		if !strings.Contains(res.Err, "api_key=***") || !strings.Contains(res.Err, "127.0.0.1:1/mcp") {
			t.Errorf("%s: the URL should stay recognisable with its values masked: %q", transport, res.Err)
		}
	}
}

// With the catalog's own text at hand, the error shows the URL as written:
// the placeholder names, never what they expanded to.
func TestURLErrorShowsURLAsWritten(t *testing.T) {
	sp := map[string]any{"type": "http", "url": "http://user:hunter2@127.0.0.1:1/mcp?api_key=sekret-value"}
	res := ProbeWith(context.Background(), "written", sp, 2*time.Second,
		Options{Written: "http://user:${DB_PASSWORD}@127.0.0.1:1/mcp?api_key=${API_KEY}"})
	if res.OK {
		t.Fatal("a closed port is not a healthy server")
	}
	if strings.Contains(res.Err, "sekret-value") || strings.Contains(res.Err, "hunter2") {
		t.Fatalf("an expanded value leaked: %q", res.Err)
	}
	if !strings.Contains(res.Err, "api_key=${API_KEY}") || !strings.Contains(res.Err, "user:${DB_PASSWORD}@") {
		t.Errorf("the URL as written should be shown: %q", res.Err)
	}
}

func TestRedactURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://h/mcp":                                 "https://h/mcp",
		"https://h/mcp?key=abc":                         "https://h/mcp?key=***",
		"https://h/mcp?key=abc&x=1&flag":                "https://h/mcp?key=***&x=***&flag",
		"https://h/mcp?key=${K}&x=1":                    "https://h/mcp?key=${K}&x=***",
		"https://u:pw@h/mcp":                            "https://u:***@h/mcp",
		"https://u:${PW}@h/mcp?t=${T}":                  "https://u:${PW}@h/mcp?t=${T}",
		"https://u@h/mcp":                               "https://u@h/mcp",
		"https://h/mcp#access_token=abc":                "https://h/mcp#access_token=***",
		"https://h/mcp?a=1#t=2":                         "https://h/mcp?a=***#t=***",
		"http://h/a b?key=x":                            "http://h/a b?key=***", // not parseable; still redacted
		"postgres://user:hunter2@db.example.com:5432/x": "postgres://user:***@db.example.com:5432/x",
	} {
		if got := redactURL(in); got != want {
			t.Errorf("redactURL(%q) = %q, want %q", in, got, want)
		}
	}
	if got := redactErr(context.DeadlineExceeded, "").Error(); got != context.DeadlineExceeded.Error() {
		t.Errorf("an error without a URL must pass through: %q", got)
	}
}

// A value expanded into the spec and echoed back — in an HTTP body, in a
// host name the resolver could not find — is put back as its reference in
// the probe's error text, which is printed and kept in measurements.json.
func TestProbeErrorPutsBackExpandedValues(t *testing.T) {
	t.Setenv("MCPICK_TEST_TOK", "SECRETVALUE42")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("invalid credential " + r.Header.Get("Authorization") + " / " + r.Header.Get("X-Attached")))
	}))
	defer srv.Close()
	raw := map[string]any{"type": "http", "url": srv.URL + "/mcp", "headers": map[string]any{"Authorization": "Bearer ${MCPICK_TEST_TOK}"}}
	sent := map[string]any{"type": "http", "url": srv.URL + "/mcp", "headers": map[string]any{
		"Authorization": "Bearer SECRETVALUE42", "X-Attached": "Bearer attached-oauth-1"}}
	res := ProbeWith(context.Background(), "s", sent, 5*time.Second, Options{Raw: raw})
	if strings.Contains(res.Err, "SECRETVALUE42") || strings.Contains(res.Err, "attached-oauth-1") {
		t.Errorf("Err = %q; the expanded and attached values must not be in it", res.Err)
	}
	if !strings.Contains(res.Err, "${MCPICK_TEST_TOK}") {
		t.Errorf("Err = %q; want the reference in place of the value", res.Err)
	}

	raw = map[string]any{"type": "http", "url": "http://nonexistent-${MCPICK_TEST_TOK}.invalid/mcp"}
	sent = map[string]any{"type": "http", "url": "http://nonexistent-SECRETVALUE42.invalid/mcp"}
	res = ProbeWith(context.Background(), "d", sent, 5*time.Second, Options{Raw: raw, Written: raw["url"].(string)})
	if strings.Contains(strings.ToLower(res.Err), "secretvalue42") {
		t.Errorf("Err = %q; the resolver's error must not carry the value", res.Err)
	}
}
