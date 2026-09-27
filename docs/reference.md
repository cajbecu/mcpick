# Reference

## Commands

```
mcpick [opts] run <cmd> [args...]   pick, render a config, launch cmd
mcpick [opts] list                  print the merged catalog
mcpick [opts] measure               measure the context cost of every server
mcpick [opts] doctor                connect to the selected servers and report
mcpick [opts] export                print the selection in an agent's dialect
mcpick [opts] serve                 run as one MCP server in front of the selection
mcpick [opts] import                copy ~/.claude.json's servers into the catalog, credentials as ${VAR} references
mcpick [opts] move <server> <group> move a server to project, local or user
mcpick [opts] profile [list]        list your profiles, the catalog's, and default
mcpick [opts] profile save <name>   save the selection (or --select) as one of yours
mcpick [opts] profile delete <name> remove one of yours, or a catalog's profile from
                                    its file (the servers stay)
mcpick [opts] profile rename <a> <b> rename one of your profiles
mcpick [opts] login <server>        run the OAuth flow for a remote server
mcpick [opts] logout <server>       forget a stored token (exit 1 when there is none)
mcpick        agents                list the agents mcpick can launch
mcpick        restore               put back project files a crashed session left rewritten
```

| Option | |
|---|---|
| `--uid ID` | session id; keys the saved selection and replaces `{UUID}` (default: hostname, which the picker's header then does not show) |
| `--file PATH` | catalog file (default: `<root>/.mcp.yaml` or `<root>/.mcp.json`) |
| `--agent NAME` | agent to render for (default: from the command name); the name is checked on every command, so a typo is an error (`mcpick agents` lists them). `--target`, its former name, is still accepted |
| `--profile NAME` | use a named profile — yours first, then the catalog's; `default` is `--all` — no picker. A catalog profile is not a check for measuring (see [Measuring](#measuring)) |
| `--select A,B` | use exactly these servers, no picker; a check on each, as the picker's. An unknown name suggests the closest ones |
| `-y`, `--last` | reuse the saved selection, no picker; a warning when none was saved, since the agent then loads no servers. A repository server whose saved check no longer covers it is dropped, with a warning (see [Measuring](#measuring)) |
| `--all`, `--none` | select everything (never a hidden server; for claude, not those disabled in Claude Code) or nothing. `--all` is not a check for measuring (see [Measuring](#measuring)) |
| `--home DIR` | where mcpick keeps its files (default: `$MCPICK_HOME`, else `~/.mcpick`) |
| `--addr HOST:PORT` | `serve` over HTTP on a loopback address instead of stdio |
| `--timeout DUR` | per-server connect timeout, a Go duration with its unit — `5s`, `2m` (default: 15s) |
| `--redact` | `move`: rewrite secrets as `${VAR:?export VAR}` references (`import` does so by default; the flag is accepted there and changes nothing); with `--yes` it is an error |
| `--yes` | `import`: copy credentials as they are, with a warning; `move`: write a server's credentials into the catalog as they are (see [Moving](#moving-a-server-between-groups)) |
| `--trust` | `measure`, `doctor`: approve the commands of stdio servers not yet trusted — what the catalog says, without a prompt; each is printed on stderr before it runs — and remember them for this project |
| `--trust-catalog` | `measure`, `doctor`: trust this project's catalog, so its remote servers are measured unselected, placeholders and private hosts included; what it trusts is printed on stderr; lapses when a catalog file changes; commands still need `--trust` |
| `--json` | machine-readable `list`, `measure`, `doctor`, `agents`, `profile list` |
| `-h`, `--help`, `-V`, `--version` | usage, version |

Options go before the command; everything after `run` belongs to the agent —
one of mcpick's own flags found there is passed to the agent and reported
(`--select after "claude" goes to claude; mcpick options go before run`).
Nothing after `--` is reported, nor the value of a flag that takes one, nor
the agent's own flags of the same name (claude's `--agent`, codex's
`--profile`). `--all`, `--none`, `--select`, `--profile` and `-y` are exclusive. The command
is looked up before anything else: `mcpick run cladue` says `cladue: command
not found (did you mean claude?)`, and a command mcpick has no adapter for
runs unchanged, with a note on how to name the agent it wraps (`--agent`).
Only `run` opens the picker, and only on a terminal; every other command, and
`run` without a terminal, uses the saved selection.

Exit status: the agent's own; 128+n when it died of signal n; 130 when the
picker was aborted; 1 for mcpick's errors, or an unreachable or skipped server
under `doctor`.

| Environment | |
|---|---|
| `MCPICK_HOME` | mcpick's home, as `--home` |
| `MCPICK_DEBUG=1` | print the exact command, with real paths, at launch |
| `MCPICK_SERVE_ALLOW_REMOTE=1` | let `serve --addr` bind a non-loopback address |
| `CLAUDE_CONFIG_DIR` | where Claude Code's config lives |

## The picker

| Key | |
|---|---|
| `↑` `↓` `j` `k`, `g` `G` (`Home` `End`), `PgUp` `PgDn` (`ctrl+u` `ctrl+d`) | move; the list scrolls with the cursor, `↑ N more` / `↓ N more` count what is out of view |
| `space` (`x`) | check / uncheck |
| `/` | filter by name or address, as you type; `enter` keeps the filter, `esc` clears it |
| `m` | measure every server in view (again; hidden ones only while their section is unfolded); pressed twice on a row marked `run?` (a command not trusted), show that command whole and ask — `y` runs and trusts it |
| `M` | review commands: show every command that is not yet trusted, whole; `y` runs and trusts them all |
| `T` | trust repo: trust this repository's catalog — show its file(s) and every remote server the trust would let `m` measure; `y` records it (an edit to a catalog file lapses it). On a trusted catalog the key reads `T untrust repo`, and `y` untrusts it |
| `a`, `n` | check all (for claude, not servers disabled in Claude Code; never hidden ones), none |
| `h`, `H` | hide / unhide the server under the cursor; unfold / fold the Hidden section |
| `p` | manage profiles: a screen of its own, see [Profiles](#profiles) |
| `s`, `l` | save the selection as a profile of yours, load one (both open the same screen) |
| `+` (`A`), `d` | add a server to the catalog, delete the highlighted one |
| `v` | move the highlighted server to another group: `p` project, `l` local, `u` user, `esc` cancels; see [Moving](#moving-a-server-between-groups) |
| `enter`, `q` | launch, abort (`esc` clears a kept filter first) |

The header shows the agent, the count, the context total of what is checked,
then what must not be missed — `N hidden checked`, or `nothing checked —
enter launches <agent> with no MCP servers` — then `uid=` when `--uid` was
given, and mcpick's version last. Under it is the command that will run. On
the first run in a project (no selection saved for this uid) the picker opens
with the local, user and plugin servers checked — never a project server,
which Claude asks about before loading — and says so; later runs start from
the saved selection. The detail line at the bottom explains the server under the cursor:
why its measurement failed, or where its credentials came from. The legend
and the key line adapt to the width: the legend shortens its words before it
drops a mark, and the key line wraps over up to three lines, so every key is
shown from about 60 columns up (two lines from about 92, one from 180). Only
narrower than that does it drop keys, the least needed first — `T`, `v`,
`H`, `M`, `d`, `+`, then movement, then `n` and `a`, then `h`, `p`, `m`, `/`
— rather than being cut. `enter` and `q` are always there, and every key
works whether or not it is shown.

The picker takes keys, not text. Text pasted into the list (or anywhere else
a letter is a command) is ignored and the status line says so; in the filter
and in the prompts a paste is inserted — its first line, without control
characters. Keys that arrive in the first 250 ms after start were typed
before the picker had drawn and are dropped, as is a burst of more than 8
printable keys within 40 ms — a paste from a terminal without bracketed
paste — which also closes a prompt it had opened itself. So that the first
keys of such a burst do nothing either, a command key waits 40 ms before it
acts; a burst that follows drops it with the rest. Each key waits on its own,
so a key held down repeats as it should.

`+` adds a server to the catalog file (`.mcp.yaml` when it exists); a name
the catalog already has, in any group, is refused at the prompt. `d` removes
a `project` server from the catalog after `[y/N]`, and a `local`
or `user` one from `~/.claude.json` after you type `yes` — that file is shared
by every Claude session on the machine. mcpick locks it, backs it up first
(`~/.claude.json.mcpick-bak-<time>`, five kept) and leaves the rest of it byte
for byte as it was. A Claude session running elsewhere rewrites the file when it
exits and can undo the deletion. Plugin servers cannot be deleted from mcpick.

### Moving a server between groups

`v` moves the server under the cursor to another group — `project`
(`<root>/.mcp.yaml` or `.mcp.json`), `local` or `user` (both in
`~/.claude.json`) — from a one-line chooser: the first letter of the group,
`esc` to cancel. `mcpick move <server> <project|local|user>` does the
same from the command line. Plugin servers cannot be moved, and nothing can
be moved into the plugin group.

The server is written to the destination first, then removed from the source,
so a failure in between leaves it in both places, never in neither; the
message then names both files. `~/.claude.json` is edited as `d` edits it:
under a lock, after a backup, the rest of the file byte for byte. A name the
destination already has is refused. Out of `user` the chooser says that
every project loses the server. The spec moves as written: `${VAR}` and
`{UUID}` stay placeholders — and when one goes into `~/.claude.json`, the
status says that Claude Code expands `${VAR}` only if the variable is set
when it starts. Moving to `local` when `~/.claude.json` has no entry for
this directory creates one, under the key mcpick already uses for it.

Into the project catalog — a file that usually sits in git — a server from
`~/.claude.json` whose spec holds credentials written out (the same
detection as `import`; see [SECURITY.md](../SECURITY.md) for what counts)
asks first: type `yes` to write the values into the file, or `r` to write
`${VAR:?export VAR}` references instead and be told which variables to
export — the status leads with them: `github → project · export
GITHUB_AUTHORIZATION before launching`. On the command line `--yes` and
`--redact` answer that question; without a terminal and without one of
them, the move is refused. With `r`/`--redact` the values end up in no file
but the backup of `~/.claude.json`.

A server disabled in Claude Code stays disabled after the move, under the
same name: its `disabledMcpServers` entry is kept where it is, in the
project record even when the server went to `user`, and the status says so.
After a move the picker keeps the cursor on the server, now under its new
heading, and keeps its check.

`h` hides a server you never use in this project. Hidden servers move from
their origin's group to a **Hidden (N)** section at the bottom, folded until
`H` unfolds it; `a` and `--all` leave them unchecked. Hiding is not
unchecking: a hidden server that is checked still loads, and the header says
`N hidden checked` until it is unchecked (`n` unchecks hidden servers too).
`m` and `M` leave hidden servers alone while the section is folded — a
hidden command must not run from a key pressed with its row out of sight —
and the status says how many were left out; unfold with `H` to measure them.
The built-in `default` profile skips them as `--all` does, and the profile
screen tags them `hidden`. The set is personal and per project, in
`~/.mcpick`, never in the catalog.

The list shows at most `max_rows` server rows at once (10 unless
[config.yaml](#configuration) says otherwise), or fewer when the terminal is
short, and scrolls to keep the cursor in view. The filter and the Hidden
section are inside the window: `N more` counts the rows that match. When
even one row would not fit, the frame gives up its blank lines, then the
legend, the key hints and the headings, in that order; the header, the
cursor row and the status line stay.

## Where servers come from

`<root>` is the nearest directory, from the current one upwards, holding
`.mcp.yaml`, `.mcp.json` or `.git`.

| Order | Source | Shown as |
|---|---|---|
| 1 | `<root>/.mcp.yaml`, then `<root>/.mcp.json` | `project` |
| 2 | `~/.claude.json` → `projects[<root>].mcpServers` | `local` |
| 3 | `~/.claude.json` → `mcpServers` | `user` |
| 4 | enabled Claude Code plugins' `.mcp.json` | `plugin`, read-only |

The groups are named as Claude Code names its scopes (`claude mcp add
--scope project|local|user`); 0.1.0 called them `workspace`, `project` and
`global`, in `list --json` too. The first definition of a name wins. A name
defined again later is reported once per pair of files — `2 servers in
.mcp.yaml shadow ~/.claude.json (a, b); the catalog wins` — unless the two
definitions are the same server (equal, or one the redacted form of the
other, as `import` leaves them), which is silent. Plugins are included
because `--strict-mcp-config` drops their servers along with everything
else. New servers added with `+` go to `.mcp.yaml` when it exists. `v`, or
`mcpick move`, moves a server between the first three groups.

## Catalog format

The same shape as Claude's `mcpServers` (`servers:` or `mcpServers:`), in
YAML or JSON, plus `profiles:` — shared: mcpick lists and loads them, `c`
copies one into yours, `d` deletes it from the file (see [Profiles](#profiles)).
See [examples/.mcp.yaml](../examples/.mcp.yaml).

Placeholders are expanded only in the config handed to the agent; the file
keeps them:

| Placeholder | Becomes |
|---|---|
| `{UUID}` | the `--uid` value |
| `${VAR}` | the environment variable, empty if unset |
| `${VAR:-default}` | the variable, or `default` if unset or empty |
| `${VAR:?why}` | an error that stops the launch if unset or empty |
| `$${VAR}` | a literal `${VAR}` |

Prefer `${VAR:?}` for credentials: an empty `Bearer ` header produces a 401
that looks like a broken server, where a missing variable names itself.
`import` and a redacting `move` write `${VAR:?export VAR}` for that reason.

Everything the catalog or a server says is printed as text: a terminal
escape in a name, an address or an error body is spelled out (`\x1b[2J`),
never sent to the terminal. `--json` is encoded JSON.

Each agent gets its own dialect — URL keys, header tables, TOML for Codex and
Grok, command arrays for opencode — and keys only one agent understands
(`bearer_token_env_var`, `includeTools`, ...) reach only that agent.

## Measuring

`m`, `measure` and `doctor` run the MCP handshake, list each server's tools and
estimate what their definitions cost in context: four bytes per token over the
serialised tool schemas. It is an estimate, close enough to see which server
takes a fifth of the window. Costs are in thousands of tokens — `4.2k`,
`250k`, and `<0.1k` for a server whose tools take under a hundred.

The picker starts with nothing measured; `m` measures every server again each
time.

**What a measurement may touch is a security rule, stated once, in
[SECURITY.md](../SECURITY.md).** In short: `m`, `measure` and `doctor` run
no stdio command you have not reviewed — the row says `run?`, `m m` or `M`
shows the command whole and `y` runs and remembers it per project;
`--trust` does the same without a prompt. A remote server the repository's
catalog defines is contacted unticked only when nothing of yours would
leave; otherwise the row says `ask`, the detail line says which condition,
and a tick — bound to that server, not to its name — is the consent.
`--all` and a catalog profile are not a check. `T` (`trust repo`) trusts
the repository's catalog as a whole, keyed by its contents, so an edit made
outside mcpick lapses it — a write of the picker's own (`+`, `d`, `v`, a
catalog profile's delete) carries it over; `T` again untrusts; `mcpick
measure --trust-catalog` records the same trust. Hidden servers are out of
reach while their section is folded. The status line after a measurement
says what was left out and what lifts it (`measured · skipped 2: …`).

The legend under the list always explains the cost and the box, and the
other marks (`401` an HTTP error, `run?`, `ask`, `···`) only while a row
shows one.

`doctor` exits 1 when a selected server was skipped, since it was not
reached. Launching is unchanged: checking a server is the consent to run it.

Failures are reduced to a word in the list — an HTTP status such as `401`
or `403`, `refused`, `dns`, `timeout`, `tls`, `no cmd`, `exited`, `env var`,
`expired`, `private`, `reset`, `closed`, else `error` — with the full
message on the detail line.

## Authentication

**Claude Code's tokens.** When measuring, mcpick borrows the token Claude Code
holds for a server, from `~/.claude/.credentials.json` or, on macOS, the
Keychain (macOS asks once; *Always Allow* keeps it quiet). Only when the
server's name and URL both match the ones the token was issued for; never
refreshed, since refreshing a rotating token would sign Claude out, so an
expired one shows as `expired`. At launch Claude uses its tokens itself.

**`mcpick login <server>`** runs the flow the MCP specification describes —
protected-resource discovery, dynamic client registration, PKCE, a loopback
redirect — and stores the token in `~/.mcpick/state/tokens.json`, keyed by name
and URL. It is attached when measuring, launching and serving, and refreshed a
minute before it expires. The URL is printed rather than opened, since mcpick
often runs where there is no browser.

A header written in the catalog wins over both.

## Profiles

A profile is a named selection. Three kinds exist:

| Kind | Where | Editable | Order |
|---|---|---|---|
| yours | `~/.mcpick/profiles.yaml` | in the picker, or `mcpick profile` | as stored; `J`/`K` |
| `default` | built-in: everything available (what `--all` selects: never a hidden server) | no | always first |
| catalog | `profiles:` in `.mcp.yaml` / `.mcp.json` | shared: mcpick lists and loads them, `c` copies one into yours, `d` deletes it from the file | alphabetical, last |

Yours are personal — one file, valid in every project — and are the only ones
mcpick writes. A personal profile with the same name as a catalog one wins
everywhere; `mcpick profile` still lists the catalog one, marked `shadowed`.
A profile naming a server this project does not have shows it under
`Missing here` and skips it at launch, with a warning on the command line.
Applying a profile never re-enables a server disabled in Claude Code; those
are left unchecked and counted, as `a` does.

`p` in the picker opens the profile manager: profiles on the left, the servers
of the highlighted one on the right.

| Key | |
|---|---|
| `tab` | switch panes |
| `+`, `c` | add a profile from the current selection; copy the highlighted one (this is how a catalog profile, or `default`, becomes yours) |
| `r`, `d`, `J` `K` | rename, delete (after `[y/N]`; the servers stay), move — yours; `d` also deletes a catalog profile from its file |
| `space` | in the right pane: toggle a server in the profile; on a `Missing here` row, drop it |
| `a`, `n` | in the right pane: every server here (never a hidden one; a profile may still name one, and its row then says `hidden`), none |
| `l`, `enter` | load the profile and go back; load it and launch |
| `esc`, `q` | back to the list, selection untouched |

Every edit is saved at once, to the file as it is on disk at that moment
(reloaded under a lock, so two pickers open at once keep each other's
changes). A `profiles.yaml` that does not parse is reported and never
overwritten. Both panes show at most `max_rows` rows
([config.yaml](#configuration)) and scroll.

A profile name is 1–40 characters: letters (any script), digits, `.`, `_`
and `-`. Anything else — a space, a slash, a pasted sentence — is refused
with the rule, in the picker's prompts and by `mcpick profile save` /
`rename`. A name stored before the rule still loads and is shown (escapes
spelled out), and can be deleted or renamed; its servers cannot be edited
until it is renamed, and nothing is rewritten under it silently.

A catalog from before `default` was built-in may define its own `default`. It
is still listed (tagged `catalog` next to the `built-in` one, and `conflict`
in `--json`) and can be loaded or copied from the picker, but `--profile
default` is refused rather than silently meaning "everything": rename it in
the catalog, or copy it with `c`.

```yaml
# ~/.mcpick/profiles.yaml
profiles:
  - name: review
    servers: [github, sentry]
```

`mcpick profile` lists all three kinds with their source — `personal`,
`catalog`, `built-in` (`--json`: an ordered list with `name`, `servers`,
`source`, `missing`); `save`, `delete` and
`rename` act on yours, and `delete` also removes a profile only the catalog
has, from the file it is in. To make a catalog profile yours from the command line:
`mcpick --profile review profile save review`.

## Configuration

`~/.mcpick/config.yaml` holds the user's defaults for the picker. The file is
optional and so is every key; mcpick never writes it.

```yaml
max_rows: 10    # server rows in view at once; the rest scroll (the profile screen's panes too)
```

A missing file is the defaults. A key mcpick does not know is ignored, so a
file written for a newer version still loads. A value it cannot use — `ten`,
`0`, a list — is reported once on stderr (`mcpick: warning: ~/.mcpick/config.yaml:
max_rows: ...`) and the default is used: a preference never stops a launch.
The loader is `internal/config`; a new key is a field there, a case in
`parse`, and a line here.

## Files

```
~/.mcpick/                       --home DIR  >  $MCPICK_HOME  >  ~/.mcpick
├── config.yaml                                UI defaults (max_rows); yours to write
├── profiles.yaml                              your profiles, valid in every project
├── selections/<workspace>-<hash>/<uid>.json   what you picked, per project and --uid: each check with the server's origin and spec hash
├── state/                                     tokens.json, measurements.json (costs for list; failures as kind + masked message), trust.json (commands and catalogs), restore/
├── state/hidden/<workspace>-<hash>.json       servers hidden with h, per project
└── run/                                       rendered configs and overlays
```

The home is `0700` and its files `0600`. Configs in `run/` hold expanded
secrets; they are named after the host and process that own them, and removed
once that process has ended. `~/.mcpick` can be shared between containers: a
launch never removes a file belonging to a process on another host until it is
clearly stale. Selections saved by pre-release builds under
`$XDG_STATE_HOME/mcpick/` are read (names only) until the next save, never
modified; nothing inside the project is read as a selection.

`--uid` keys the selection, so several agents sharing one project keep their
own; it defaults to the hostname, which inside a container is unique per box.

## `mcpick serve`

```sh
mcpick --profile review serve                    # stdio
mcpick --profile review serve --addr 127.0.0.1:7000
```

mcpick as one MCP server in front of the selection: point an agent at it once,
and changing the selection never touches that agent's config again. Tools are
named `<server>__<tool>`, within the 64-character limit clients enforce. A
dead upstream hides its tools instead of breaking the list, and is retried
after 30 seconds. Only tools are aggregated, not resources or prompts. Two
tools that come out under the same name — `a.b` and `a_b` sanitise alike —
are not both listed, since clients reject a list with a name twice: the
server first in the selection keeps it, and the other tool is left out and
named once on stderr.

The proxy has no authentication of its own, so `--addr` accepts only loopback
addresses and requests with a non-local `Origin` are refused.
`MCPICK_SERVE_ALLOW_REMOTE=1` lifts the first rule; use it only behind your
own authentication.

## Code layout

```
main.go             hands off to internal/cli
internal/cli        argument parsing, one function per command
internal/config     ~/.mcpick/config.yaml, the user's defaults for the picker
internal/catalog    .mcp.yaml/.mcp.json, ~/.claude.json and plugins
internal/spec       the server entry and the shared JSON and TOML dialects
internal/backend    one file per agent, registered at start-up; the flag,
                    overlay and project-file mechanisms they share
internal/mcp        MCP client (stdio, streamable HTTP, legacy SSE), measuring
internal/proxy      mcpick serve
internal/oauth      OAuth, mcpick's tokens and Claude Code's
internal/state      the saved selection, and the hidden set (hidden.go)
internal/profile    the user's own profiles
internal/trust      what a measurement may run: trusted commands (trust.go),
                    harmless repository remotes (remote.go), trusted
                    catalogs (catalog.go)
internal/tui        the picker (tui.go); hide.go the Hidden section,
                    scroll.go the window, profiles.go the profile manager,
                    trust.go the m m / M screens, catalogtrust.go the T
                    screen, move.go v, paste.go the paste and typeahead
                    guards, frame.go the cut to the window
internal/fsutil     atomic writes, the home directory, locks
internal/proc       exec, signals, process groups
internal/testguard  a check, run from TestMain, that no test touched the
                    real ~/.claude.json
e2e                 end-to-end tests in Docker, a module of their own
                    (`just e2e`; see CONTRIBUTING.md)
```
