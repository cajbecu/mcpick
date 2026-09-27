# Security

## Reporting

Report vulnerabilities through GitHub's private advisory form
("Security" → "Report a vulnerability") rather than a public issue.

## What mcpick touches

mcpick handles credentials, so it is worth being explicit about where they go.

**Rendered configs contain expanded secrets.** A catalog entry holding
`Authorization: Bearer ${TOKEN}` becomes a real token the moment mcpick renders
it for an agent. Those files are written mode `0600` inside `~/.mcpick/run/`
(or wherever `--home` / `MCPICK_HOME` points), a `0700` directory under a
`0700` home. mcpick refuses either directory if it is a symlink or owned by
another user, so a directory planted in advance cannot collect secrets. They
are on disk, not in RAM; point the home at a tmpfs to change that. mcpick
replaces its own process with the agent and has no exit hook; each file is
named after the host and process that own it, and the next launch removes the
files of processes on this host that have ended.

**The catalog should not contain secrets.** Use `${VAR}` references and export
the variables. `${VAR:?why}` fails the launch instead of rendering an empty
credential, which is what turns a missing token into a clear error rather than
a confusing `401`.

**`mcpick import` and a move into the project catalog redact.** Both write
into a file that usually sits in a git repository, so a credential written in
a spec is rewritten as `${NAME:?export NAME}` — a reference that fails the
launch by name when the variable is unset, never an empty `Bearer ` — and the
command names the variables to export. What counts as a credential: a value
under a secret-sounding key (`Authorization`, `API_KEY`, `x-api-key`,
`GITHUB_TOKEN`, …, matched by word, so `KEY_FILE`, `CLIENT_ID` and
`--no-auth` are not); the value of such a flag in `args` (`--api-key VALUE`,
`--token=VALUE`) or of a `K=V` argument; the password of a
`scheme://user:password@host` URL anywhere, `DATABASE_URL` in `env` included;
a secret-sounding query parameter in a URL; a token with a well-known
prefix (`ghp_`, `gho_`, `github_pat_`, `sk-`, `sk_live_`, `xoxb-`, `xoxp-`,
`AKIA`, `glpat-`) wherever it is written; a value of the form `Bearer`,
`Basic` or `Token` followed by at least 8 characters, under any key
(`AUTH_HEADER`, `X-Custom`) or as an argument; and a header passed as
`-H 'Name: value'` or `--header`, by its name or its form. A `${VAR}`
reference is left as written, and so is a match that holds one; the rest
of the value is still searched. The values stay where they were
(`~/.claude.json`, or its backup after a move). `import --yes` copies the
values as they are and warns, naming what it copied; `move` asks on a
terminal and refuses without one. Detection is a guess, so when a value was
not a secret the cost is one export; when it was, the cost of missing it is
a token in git.

**`mcpick export` prints expanded credentials.** Its output is the rendered
config, so `${TOKEN}` is the token there; it says so once on stderr ("export
wrote expanded credentials to stdout; keep it out of files you commit"), and
says when nothing is selected.

**What mcpick prints is text.** Names, addresses and profile names come from
the catalog; an error body, a server's name and a stdio server's last words
on stderr come from the server. Every line that reaches the terminal — the
tables of `list`, `doctor` and `measure`, the profile and move output, the
warnings, the top-level error, the picker's rows, status line and warnings —
goes through one sanitiser that spells out every character that would not
show (`\x1b`, `\u009b`, `\u202e`), so a `500` body carrying OSC 52 cannot
write your clipboard and a C1 CSI cannot clear the screen. `--json` and
`serve` are encoded JSON, which is its own escaping.

**Errors carry no expanded secrets.** A transport error names the URL the
request went to, which is the catalog's URL with your environment expanded
into it. mcpick shows the URL as the catalog wrote it (`?key=${KEY}`), and
where only the expanded one is at hand (`serve`) masks its query values and
userinfo password. `~/.mcpick/state/measurements.json`, which `measure` and
`doctor` write so that `list` can show costs without connecting (the picker
never reads it), keeps a failure as its kind and a message with URL
passwords, secret-sounding query values and known tokens masked.

**Claude Code's tokens are read, never written.** To measure a server Claude is
signed in to, mcpick reads the token Claude stored for it
(`~/.claude/.credentials.json`, or the Keychain on macOS), sends it only to the
URL it was issued for, keeps it in memory only, and never refreshes it.

**OAuth tokens are stored in plain JSON**, mode `0600`, in `~/.mcpick/state/tokens.json`.
They are not encrypted and not in a system keychain. The authorization flow uses
PKCE and checks the `state` parameter on the callback, and the loopback listener
binds `127.0.0.1` on an ephemeral port registered for that one exchange.

**Project files are restored from records in `~/.mcpick/state/restore/`.** A record
names the host and process that wrote it; a launch only recovers records from
its own host whose process has ended, because workspaces are often shared
between containers whose process ids mean nothing to each other.

**Project-file agents get the selection written into the repository.**
gemini, grok and devin have no per-run override, so mcpick rewrites their
project config (`.gemini/settings.json`, `.grok/config.toml`,
`.devin/mcp_config.json`) for the run, expanded — `${VAR}` values included —
and restores it on exit. A file mcpick creates is mode `0600`, in a
directory it creates `0700`. A file that already exists and that others can
read is `0600` for the run, and the launch says so; its own mode comes back
with its contents on exit, or at the next launch after a crash. Gemini
expands `$VAR` and `${VAR}` in a server's `env` block itself, so an env
value made of text and plain `${VAR}` references is written as the catalog
has it and the value never reaches the file; `${VAR:?why}` and
`${VAR:-default}` (what `import` writes) are written as `${VAR}` once `VAR`
is set and not empty. A default in use, `$${VAR}`, `{UUID}` and every other
key — headers, url, args — are written expanded, as is everything for the
other two agents. A crash
leaves the rewritten file in place until the next launch or `mcpick
restore` recovers it. The Antigravity CLI is not supported and is launched
unchanged: nothing is written for it (docs/agents.md).

**Overlay directories are symlinks into your real config directory.** When
mcpick redirects `CODEX_HOME` or `XDG_CONFIG_HOME`, the agent still reads your
actual credential files through those links. The overlay is not a sandbox and is
not meant to be one — it exists so redirecting the config directory does not log
you out. A file the agent wrote into the overlay that cannot be brought back
when it exits — a refreshed login, a session log — is kept under
`~/.mcpick/state/recovered/<timestamp>/`, which nothing prunes, and the
launch says where.

**`mcpick serve` does not authenticate.** With `--addr` it listens on plain
HTTP and forwards every call to the upstream servers with their credentials.
It therefore refuses any address that is not loopback, unless
`MCPICK_SERVE_ALLOW_REMOTE=1` is set, and it rejects HTTP requests whose
`Origin` is not local, as the MCP specification requires, so a web page cannot
drive it through DNS rebinding. The stdio transport, which is the default, has
no such exposure.

**No command runs because you pressed `m`.** Measuring a stdio server runs
its command, and a repository — or a plugin, or a stale entry in
`~/.claude.json` — can put any command there. mcpick therefore runs no stdio
server's command until you have seen it and trusted it: `m` on the row shows
the command, a second `m` opens it whole — wrapped to the terminal's width,
never cut, terminal escapes and non-ASCII spelled out — and `y` runs it and
remembers that; `M` does the same for every command that would run. The
consent screens show the command, its arguments and its environment exactly
as the catalog wrote them, nothing masked: it is your own terminal, and
consent means seeing what runs. Output nobody consents on — the skip
warnings on stderr, `--json`, the list's detail line — masks arguments and
env values that look like credentials instead. `mcpick measure --trust` and
`doctor --trust` approve what the catalog says without a prompt; they print
each command they approve on stderr, arguments and env values that look
like credentials masked, before running it; `measure --trust` leaves hidden
servers out, and names them. Trust is kept in `~/.mcpick/state/trust.json` per
project, server name and SHA-256 of the command, its arguments and its
environment — names *and values*, as written in the catalog, so `${TOKEN}`
hashes as the placeholder and rotating the secret behind it does not re-ask;
only the hash is stored, never a value — so a changed command, argument or
variable asks again. A command with a path separator (`./run.sh`) is
resolved against the directory mcpick runs from before it is shown and
hashed, so the same catalog line from another directory asks again. The
contents of what a command refers to — a script, a package `npx` or `uvx`
fetches, an interpreter on `PATH` — are not hashed: trust covers the command
line as written, not what it loads. An entry whose `url` expands to nothing
is not probed either: as written it is remote and needs no trust, but with
the variable unset its command would run in the URL's place. Launching is
unchanged: checking a server is the consent to run it.

**A repository's remote server is measured unchecked only when nothing of
yours leaves.** Measuring a remote server expands your environment into its
URL and headers and sends the request to a host the repository's author
chose. So a remote server the project's own catalog (`.mcp.yaml`,
`.mcp.json`) defines is measured by a bare `m`, `mcpick measure` or
`doctor` without being checked only when all of these hold, each for a
reason:

- *Its URL and every header value, as written, hold no `${VAR}`* — then the
  request carries only what the file already holds; a literal
  `Authorization: Bearer xxx` is the author's, not yours. `$${` is the
  escape for a literal and is allowed. The rule is on the catalog text, not
  on what a placeholder expands to, so an unset variable is no way through.
  `{UUID}` counts too: it becomes `--uid`, by default this machine's
  hostname.
- *Its host is not local or private* — `localhost`, `*.localhost`, `*.local`,
  `*.internal`, `*.lan`, `*.home.arpa`, a name without a dot, and IP
  literals that are loopback, private (10/8, 172.16/12, 192.168/16),
  link-local, carrier-grade NAT (100.64/10), IETF protocol assignments
  (192.0.0.0/24), benchmarking (198.18/15), reserved (240/4), unspecified
  (and 0/8), multicast or broadcast, IPv6 unique-local (fc00::/7),
  site-local (fec0::/10), link-local (fe80::/10) or local-use NAT64
  (64:ff9b:1::/48), and the IPv4-mapped, IPv4-compatible and NAT64 forms of
  these. A URL whose host cannot be made out is treated the same
  (`address not understood`). A request the author aims at your own machine
  or network reaches a service there with whatever headers the author
  wrote, which is not "nothing of yours".
- *And the address actually dialled is public too* — a public name can
  resolve to 127.0.0.1 (DNS rebinding, `*.nip.io`), and a public host can
  redirect into the LAN. These measurements therefore go through a dialer
  that checks the resolved address of every connection, redirects included,
  and refuses a private one; the row then says `private`.
- *No proxy applies to the URL* — with `HTTP_PROXY`, `HTTPS_PROXY` or
  `ALL_PROXY` in force the connection goes to the proxy and the address
  reached cannot be checked, so such a server is left to a check. The
  checked dialer never uses a proxy.

A repository remote that fails one of these keeps the older rule: its row
says `ask` (measuring it would send something of yours), the detail line
says which condition (`sends ${VAR} to its host`, `sends {UUID} to its
host`, `private address`, `through a proxy`, `address not understood`), and
ticking it is the consent — a check bound to that server, see below. OAuth tokens are unchanged: a token is attached
only on an exact name-and-URL match, the issuer's own URL.

**A check is consent for one server, not for a name.** The saved selection
(`~/.mcpick/selections/<workspace>-<hash>/<uid>.json`) records, for each
checked server, where it came from and a SHA-256 of its spec as written
(`${VAR}` unexpanded, so no value is stored). A server the repository's
catalog defines counts as checked — for measuring it, and for handing it to
the agent on `run -y` — only while both are what they were. So a repository
server that takes the name of a user server you had checked does not
inherit the check (the shadow warning names it too, unless the two
definitions are the same server), and a checked repository server whose URL or headers
changed since asks again. The picker opens such a row unchecked and says
why on its detail line ("github now comes from this repository's catalog;
check it again"); `-y`, `measure` and `doctor` drop it with a warning on
stderr that names the server and the reason. Checking it again, or naming
it with `--select`, is the new consent, for the server as it is now. Your
own servers — local, user, plugin — keep their check when you edit
them: they are yours. Selections saved by earlier versions hold names only;
their repository servers are unconfirmed until checked again.

**`--all` and a catalog profile are not a check.** Both select every server
the repository lists, and launching with either is the consent to launch,
as it was. But neither names a server, and a repository can put anything
under either, so `mcpick --all measure` and `mcpick --profile <catalog
profile> measure` (and `doctor`) measure a repository remote that is not
harmless no more than a bare `m` does: it is skipped, and the warning says
so. Measuring one takes the explicit forms — a check in the picker,
`--select NAME`, a profile of your own — or `T` / `--trust-catalog`.

**Trusting the repo.** `T` in the picker (`trust repo`), or `--trust-catalog`
with `measure` / `doctor`, trusts the project's catalog as a whole: from then
on `m` measures every remote server it defines, placeholders and private
hosts included and without the dial-time restriction, because you have seen
the files and said so. The screen (and the flag, on stderr) lists the catalog
file(s) and every remote server the trust covers, with its URL and header
names — a literal value masked, a `${VAR}` shown, since that is yours. Stdio
commands are not covered: each still needs its own trust. The record lives
in `~/.mcpick/state/trust.json` (mode `0600`) under the project root and a
SHA-256 over the bytes every catalog file held when the catalog was parsed —
the catalog on screen, not the files as they are on disk when `y` is
pressed: if a file changed in between, `T` refuses with "the catalog changed
on disk; reload to review it" and records nothing, and `--trust-catalog`
parses and hashes one snapshot. Any edit to `.mcp.yaml` or `.mcp.json` made
outside mcpick — a pull, a rebase, a file appearing, an editor — lapses it
on the next load: the rows go back to `ask`, the status line says "catalog
changed since you trusted it; T to trust it again", and `T` shows the files
again. A write of the picker's own — `+`, `d`, `v`, a catalog profile's
delete — carries the trust over to the new contents, since you made the
edit on screen; the status line says so, and only when the files on disk
were still the bytes the picker loaded — an outside edit before the write
lapses it as any other. `T` on a trusted catalog (`T untrust repo` on the
key line) offers to untrust it. The Project heading says `trusted` while it
is in force.

**The first run checks only your own servers.** With no selection saved for
a project and uid, the picker opens with the local, user and plugin servers
checked — what the agent loads without mcpick — minus those disabled in
Claude Code (for claude) and those hidden here, and never a server the
repository's catalog defines: a tick on one of those is the consent above,
and it is yours to give. With nothing checked the header says that enter
launches with no MCP servers.

**What launching writes.** Nothing into your project for most agents: the
rendered config is under `~/.mcpick/run/`. For gemini, grok and devin their
project file is rewritten for the run and put back when they exit (above).
In the picker, `+`, `d` and `v` edit the catalog and `~/.claude.json` (with
a backup); `h`, `p`, `T`, `m m`/`M` and a launch write under `~/.mcpick`
only.

**MCP servers are not sandboxed by mcpick.** Selecting fewer of them is a real
reduction in what a prompt injection can reach, which is part of the point, but
mcpick does not inspect, filter or contain what a server does once it is
selected.
