# Changelog

All notable changes to this project are documented here.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and the project uses [semantic versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.2.0] - 2026-09-27

### Upgrading from 0.1.0

Behaviour that changed; each is detailed under Changed and Security below.

- Profiles are personal, in `~/.mcpick/profiles.yaml`; a catalog's `profiles:` key is still read, and `c` copies one of its profiles into yours.
- `list --json` names the groups `project`, `local`, `user` and `plugin` in `origin` (0.1.0: workspace, project, global); `profile --json` is an ordered list with `source` and `missing`.
- `doctor` exits 1 when a selected server was skipped, not only when one was unreachable.
- A stdio server's command is not run by `m`, `measure` or `doctor` until it has been reviewed (`m m`, `M`, `--trust`); the row says `run?` in the meantime.
- `--agent NAME` and `mcpick agents` replace `--target` and `targets`, which are kept as aliases.
- `import` redacts credentials by default; `--yes` copies them as they are.
- `--all` (and `a`) is built from the catalog as it is now, never from the saved selection, and leaves hidden and Claude-disabled servers out.
- `default` is a built-in profile — everything available — and cannot be the name of a personal one; a catalog that defines its own `default` is listed apart and `--profile default` is refused while it does.
- Profile names are checked: 1–40 characters of letters, digits, `.`, `_` and `-`; a name stored before the rule is still shown and can be deleted or renamed.
- Servers disabled in Claude Code count as disabled only when launching `claude`; for every other agent they are ordinary servers.
- The Antigravity CLI (`agy`) is not supported: `mcpick run agy` launches it unchanged with a notice; `export --agent antigravity` still writes the IDE's file shape.
- `logout` of a server with no stored token exits 1.
- Selections are read from `~/.mcpick/selections` (where 0.1.0 saved them) and, names only, from `$XDG_STATE_HOME/mcpick/`; nothing inside the project — `<root>/.tmp/`, `<root>/.scratchpad/mcpick/` — is read as a selection any more.

### Added

- `v` in the picker and `mcpick move <server> <project|local|user>` move a server between the project catalog and the local and user servers of `~/.claude.json`: destination written first, a name already there refused, placeholders kept, a typed `yes` or `r` (`--yes` / `--redact`) for credentials moving into the catalog, the cursor and the check kept.
- `h` hides the server under the cursor into a folded **Hidden (N)** section that `H` unfolds; `a`, `--all` and `default` leave hidden servers unchecked, `m` and `M` leave them alone while folded, a checked one still loads and the header says so; the set is per project under `~/.mcpick/state/hidden/`.
- The list scrolls: at most `max_rows` rows in view (10, fewer on a short terminal), `↑ N more` / `↓ N more` at the edges; a very short terminal drops blank lines, legend and key hints before the cursor row.
- `~/.mcpick/config.yaml` holds the picker's defaults (`max_rows`); a bad value is a warning, never a failed launch.
- Profiles of your own in `~/.mcpick/profiles.yaml`, valid in every project, a built-in `default` that is everything available, and a profile manager (`p`): `+` add, `c` copy, `r` rename, `d` delete (a catalog profile too, from its file), `J`/`K` reorder, `space` toggle, `l` load, `enter` launch; `mcpick profile rename`.
- The first run in a project opens with the local, user and plugin servers checked — what claude loads without mcpick; for another agent the status says these are the servers Claude Code loads — never a project server; with nothing checked the header says `nothing checked — enter launches <agent> with no MCP servers`.
- `--trust` and `--trust-catalog` for `measure` and `doctor`; `measure --json` and `doctor --json` list what was skipped and why.
- `mcpick run cladue` says `cladue: command not found (did you mean claude?)`, and `mcpick run --all claude` that mcpick options go before `run`; a command with no adapter runs unchanged with a note on how to name the agent it wraps; `--select` with an unknown name suggests the closest ones.
- One of mcpick's own flags after `run <cmd>` is reported (`--select after "claude" goes to claude; mcpick options go before run`) and still passed to the agent; nothing after `--`, no flag's value and none of the agent's own flags of the same name (claude's `--agent`, codex's `--profile`) is reported.
- `-y` with no saved selection warns that the agent will load no servers; `export` warns when nothing is selected and when its output holds expanded credentials.
- The key line wraps over up to three lines so every key is on screen at ordinary widths; only a narrower terminal drops keys, the least needed first.
- `-V` (`--version`) in the usage text, the man page and the completions.
- The completions take server names from a hidden `mcpick __complete servers` (one plain name per line), for `move` and `--select` in bash, zsh and fish; they used `list`'s first column, headings included.
- End-to-end tests in Docker (`just e2e`): every agent CLI that installs headless is launched through the real picker against fake MCP servers and checked to load exactly the chosen ones.
- [docs/use-cases.md](docs/use-cases.md), [docs/decisions.md](docs/decisions.md); the README is a page again, with the options, environment and files tables in [docs/reference.md](docs/reference.md) only.

### Changed

- `--agent NAME` names the agent and `mcpick agents` lists them; `--target` and `targets` stay as aliases; the name is checked on every command (decision #63).
- The groups are named as Claude Code names its scopes — `project`, `local`, `user`, `plugin` — in the picker, `move`, `list` and the `origin` values of `list --json` (decision #61).
- The picker's header leads with the agent, the count, the context total and the warnings; `uid=` shows only when `--uid` was given, the version last.
- `mcpick import` redacts by default, writing `${VAR:?export VAR}` and naming the variables; `--yes` copies the values with a warning; `--redact` is accepted and changes nothing (decision #59); its output says how to remove the copies from `~/.claude.json`.
- A name defined in the catalog and again in `~/.claude.json` is reported on one line per pair of files (`2 servers in .mcp.yaml shadow ~/.claude.json (a, b); the catalog wins`), and not at all when the two definitions are the same server or one the redacted form of the other.
- antigravity: the `agy` CLI is not supported — it reads `~/.gemini/config/mcp_config.json` and plugins only — so `mcpick run agy` launches it unchanged with a notice and writes nothing into the project; `export --agent antigravity` still writes the IDE's workspace file shape (decision #64).
- `--all`, `--none`, `--select`, `--profile` and `-y` are exclusive; so are `--redact` and `--yes` for `import` and `move`.
- `doctor` exits 1 when a selected server was skipped, not only when one was unreachable.
- `/` filters as you type on a line above the list; `enter` keeps the filter, `esc` clears it; the `… 1-10 of 30` line is replaced by the edge counts.
- The picker runs on the terminal's alternate screen; on exit the terminal is as it was.
- Servers disabled in Claude Code are handled as such only when launching `claude`; for other agents they are ordinary servers.
- Each agent is one file in `internal/backend` that registers itself (see CONTRIBUTING.md).
- Release archives and the Linux packages carry `SECURITY.md`; the `.deb` and `.apk` now carry `LICENSE`, `README.md` and `SECURITY.md` under `/usr/share/doc/mcpick` (they were marked rpm-only).
- Profiles are personal: `s` and `mcpick profile save` write to `~/.mcpick/profiles.yaml`; a `profiles:` key in the catalog is shared — mcpick lists and loads them, `c` copies one into yours, `d` deletes it from the file; a personal profile of the same name wins.
- Applying a profile, `a` and `--all` never re-enable a server disabled in Claude Code; `--profile default` is refused while the catalog defines its own `default`.
- `mcpick profile --json` is an ordered list with `source` and `missing`; profile names are 1–40 characters of letters, digits, `.`, `_` and `-`, a name stored before the rule is still shown and can be deleted or renamed.
- One wording: `list` says `(disabled in Claude Code)`, the usage text says picker, and it is `built-in` everywhere.
- The `v` status leads with the move and the export to do (`github → project · export GITHUB_AUTHORIZATION before launching`); out of `user` the chooser says every project loses the server.
- After a measurement the status keeps what was left out: `measured · skipped N: …`.
- Context costs are always in thousands — `0.3k` where 0.1.0 said `320t`, `<0.1k` under 100 tokens — and a server with one tool says `1 tool`.
- `mcpick agents` names the files an agent also reads as `~/…` and `./…`, not `{home}`, `{root}` and `{xdg}`.
- `--timeout 5` says the duration needs a unit (`e.g. 5s`).
- `logout` of a server with no stored token exits 1.
- A selection saved by a pre-release build under `<root>/.scratchpad/mcpick/` or `<root>/.tmp/` is no longer read (decision #70).

### Fixed

- `gemini mcp list` and gemini's other subcommands no longer get `--allowed-mcp-server-names`, which made them fail.
- devin: the selection goes to `.devin/mcp_config.json`, the file Devin CLI 3 reads; written to `.devin/config.json` it survived mcpick's restore.
- muse: the generated settings file carries `schema_version` and uses `mcpServers`; muse rejected the previous one.
- `d` on a project server defined in `.mcp.json`, next to a `.mcp.yaml`, deletes it from `.mcp.json`; it used to report success and the server came back.
- `+` with a name the catalog already has, in any group, is refused; it used to duplicate the row and write over the file's entry.
- The M and T screens no longer panic when the window is resized after scrolling.
- A key held down repeats as it should; each held key waits its 40 ms on its own, where a wait restarted by every repeat froze the cursor until the key was let go.
- Backups of `~/.claude.json` are named to the nanosecond and never overwritten.
- Two mcpick processes refreshing the same OAuth token no longer both spend its refresh token (a rotating server then logs you out): a lock file per token, next to `tokens.json`, is held from the re-read to the save.
- A catalog that shares a spec through a YAML anchor is no longer corrupted by `d`, `v` or a catalog profile's delete: an edit that would leave an alias without its anchor is refused, a move before it writes anything, and every catalog write is read back before it lands (decision #72).
- A URL pasted into the add-server prompt is taken; the prompts accept non-ASCII and backspace removes a whole character.
- The picker header no longer repeats up the screen: every line is cut to the window's width and the frame to its height.
- Text not meant for the picker is no longer read as keys: a bracketed paste where keys are commands is ignored, keys in the first 250 ms after start are dropped, and a burst of more than 8 printable keys within 40 ms is dropped too, closing a prompt it had opened.
- The leaks warning (servers an agent loads from files mcpick does not control) is no longer hidden by a scalar key such as opencode.json's `$schema`.
- `serve`: two upstream tools that come out under one name (`a.b` and `a_b` sanitise alike) no longer make a tool list clients reject; the server first in the selection keeps the name, the other tool is left out and named once on stderr.

### Security

- Measuring runs no stdio server's command until you have reviewed it — your own included: the row says `run?`, `m m` or `M` shows the command whole (arguments quoted, escapes spelled out, nothing cut or masked) and `y` runs and remembers it per project, server name and hash of command, arguments and environment as written; `measure --trust` / `doctor --trust` approve without a prompt, printing each command first; launching is unchanged.
- A remote server from the project's own catalog is measured unchecked only when nothing of yours would leave — no `${VAR}` or `{UUID}` in URL or headers, no local or private host, no proxy — and then onto a public address only, redirects included; otherwise its row says `ask` and a tick is the consent.
- A check is bound to the server it was given to (origin and spec hash in the saved selection), not to its name: a repository server that took a checked name, or changed, opens unchecked with the reason and is dropped by `-y`, `measure` and `doctor` with a warning.
- `--all` and a catalog profile are not a check for measuring; a check in the picker, `--select`, a personal profile or `--trust-catalog` is.
- `T` (`trust repo`) trusts the project's catalog as a whole, keyed on the bytes the picker parsed; it refuses a catalog that changed on disk since the load; an edit made outside mcpick lapses it, a write of the picker's own (`+`, `d`, `v`, a catalog profile's delete) carries it over with a note, and only the rows it wrote take their spec from the file — a file that holds anything else is not adopted, the trust is not held and the status asks for a reload (decision #69); `T` again untrusts; `mcpick measure --trust-catalog` records the same.
- Project files written for gemini, grok and devin are created `0600` in a `0700` directory; a file that exists and that others can read is `0600` for the run and gets its mode back on exit (decision #74); gemini gets `${VAR}` env references unexpanded, `${VAR:?why}` and `${VAR:-default}` as `${VAR}` once `VAR` is set (decisions #55, #75).
- Every line printed from the catalog or a server goes through one sanitiser that spells out terminal escapes; `--json` and `serve` are encoded JSON, with C1 controls and format characters (bidi overrides, zero-width and tag characters) escaped as `\uXXXX` too.
- Errors carry no expanded secrets: the URL is shown as the catalog wrote it (in `serve` too), a value expanded into the spec and echoed back — an HTTP body, a DNS error, a server's stderr — is put back as its `${NAME}`, an attached token as `***`, and `measurements.json` keeps a failure as its kind and a masked message (decisions #58, #73).
- Secret detection (`import`, a move into the catalog) also finds credentials in `args`, `K=V` arguments, URL userinfo, query and fragment parameters, tokens with well-known prefixes, `Bearer`/`Basic`/`Token` values under any key and `-H 'Name: value'` headers in args; keys are matched by word; a `${VAR}` in a value hides only the match that holds it (decision #71).
- The `--trust` notice masks credentials in arguments as well as env values, and URL passwords, secret query values and tokens anywhere, as `list` now does in its endpoint column (text and `--json`); `measure --trust` leaves hidden servers out and names them; `mcpick restore` runs behind the home ownership check; an entry whose `url` expands to nothing is not probed.
- A repository can no longer ship a selection: `<root>/.tmp/mcpick-<uid>.json`, with pre-checked servers and forged `checks`, is not read; a legacy selection outside the project gives its names only (decision #70).
- 192.0.0.0/24, 198.18.0.0/15, 240.0.0.0/4, the site-local fec0::/10 and the local-use NAT64 prefix 64:ff9b:1::/48 count as private addresses for the unchecked measurement of a repository's remote server, on the literal and at dial time.

## [0.1.0] - 2026-09-24

First release.

### Picking

- A picker in front of the agent: every MCP server mcpick can see, grouped by
  where it is defined. `m` measures what each one costs in the model's context
  window, with a total for the selection; `/` filters; `s` and `l` save and load
  profiles.
- The header shows mcpick's version and the agent as a badge in its colours
  (`✻ claude`, `◉ codex`, `✧ gemini`, ...), and under it the exact command that
  will run. `MCPICK_DEBUG=1` prints the real one, with real paths, at launch.
- A failed measurement shows its kind in the list (`401`, `refused`,
  `timeout`, `dns`, `tls`, `expired`, ...) and the full message, with the fix
  when there is one, on the detail line. A one-line legend explains the marks.
- Servers are merged from `.mcp.yaml` and `.mcp.json` in the project, the
  project and global entries of `~/.claude.json`, and the servers of enabled
  Claude Code plugins, which `--strict-mcp-config` would otherwise drop.
- A server disabled in Claude Code says so; checking it re-enables it in Claude
  Code at launch. The picker opens with such servers unchecked, and neither `a`
  nor `--all` checks them.
- Non-interactive selection with `--select a,b`, `--profile NAME`, `--all`,
  `--none` and `-y` (the saved selection).
- Profiles live in the catalog and travel with the project; `mcpick profile`
  lists, saves and deletes them.
- Placeholders in the catalog: `{UUID}`, `${VAR}`, `${VAR:-default}`,
  `${VAR:?why}` (fails the launch instead of rendering an empty credential)
  and `$${VAR}`. `mcpick import --redact` seeds the catalog from
  `~/.claude.json` with credentials rewritten as `${VAR}`.

### Launching ten agents

- `claude` through `--mcp-config` and `--strict-mcp-config`.
- `codex`, `copilot`, `pi`, `muse` and `opencode` through an overlay of their
  config directory: every entry is a symlink to the real one except the
  generated config, and whatever the agent writes during the session is carried
  back on exit.
- `gemini`, `antigravity`, `grok` and `devin` by rewriting the project config
  for the run and restoring it on exit, keeping edits made from inside the
  agent. Concurrent sessions on one file are refused and a crashed session is
  recovered on the next launch; `mcpick restore` does it by hand.
- Each agent gets its own dialect: URL keys, header tables, TOML for Codex and
  Grok, command arrays for opencode, keys only one agent understands kept away
  from the others.
- Before a launch, mcpick names the servers an agent will load anyway from
  files it reads on its own, when the selection cannot be exclusive.

### Measuring and authentication

- Measuring borrows the token Claude Code holds for a server —
  `~/.claude/.credentials.json`, or the Keychain on macOS — so a server Claude
  is signed in to shows its cost instead of `401`. Only when name and URL both
  match; never refreshed.
- `mcpick login` and `logout` run the MCP OAuth flow — discovery, dynamic
  client registration, PKCE, loopback redirect — and store tokens that are
  attached and refreshed automatically.
- Servers defined by a project's own catalog are measured only once selected,
  so a cloned repository cannot run a command, or send a secret from your
  environment to its author's host, because you pressed `m`.
- The MCP client speaks stdio, streamable HTTP and the legacy HTTP+SSE
  transport.

### Other commands

- `mcpick doctor` connects to the selected servers and reports tool counts,
  context cost, latency and the reason for each failure.
- `mcpick measure` measures every server; `mcpick list` shows the catalog.
- `mcpick serve` runs mcpick as one MCP server in front of the selection, over
  stdio or loopback HTTP, with tools namespaced `<server>__<tool>`.
- `mcpick export --target NAME` prints the selection in any agent's dialect;
  `mcpick targets` lists the agents, how each is reached and the files each also
  reads.

### Files

- Everything mcpick writes lives in `~/.mcpick` (`--home` or `MCPICK_HOME`):
  `selections/`, `state/` for tokens, measurements and restore records, and
  `run/` for rendered configs, which hold expanded secrets, are `0600` in a
  `0700` directory, and are removed once their session has ended. Nothing is
  written into the project.
- `~/.mcpick` can be shared between containers: runtime files are named after
  host and process, so one box never removes another's live config.
- Edits to `~/.claude.json` keep its key order and content byte for byte, take
  a lock, and leave a backup.

### Distribution

- Linux, macOS (one universal binary) and Windows builds; `.deb`, `.rpm` and
  `.apk` packages with the man page and shell completions; a Homebrew cask in
  `cajbecu/tap`; a Nix flake; checksums and build-provenance attestations.

[Unreleased]: https://github.com/cajbecu/mcpick/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/cajbecu/mcpick/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/cajbecu/mcpick/releases/tag/v0.1.0
