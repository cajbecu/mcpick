# Reference

## Commands

```
mcpick [opts] run <cmd> [args...]   pick, render a config, launch cmd
mcpick [opts] list                  print the merged catalog
mcpick [opts] measure               measure the context cost of every server
mcpick [opts] doctor                connect to the selected servers and report
mcpick [opts] export                print the selection in an agent's dialect
mcpick [opts] serve                 run as one MCP server in front of the selection
mcpick [opts] import                copy ~/.claude.json's servers into the catalog
mcpick [opts] profile [list]        list the catalog's profiles
mcpick [opts] profile save <name>   save the selection (or --select) as a profile
mcpick [opts] profile delete <name> remove a profile
mcpick [opts] login <server>        run the OAuth flow for a remote server
mcpick [opts] logout <server>       forget a stored token
mcpick        targets               list the agents mcpick can launch
mcpick        restore               put back project files a crashed session left rewritten
```

| Option | |
|---|---|
| `--uid ID` | session id; keys the saved selection and replaces `{UUID}` (default: hostname) |
| `--file PATH` | catalog file (default: `<root>/.mcp.yaml` or `<root>/.mcp.json`) |
| `--target NAME` | agent to render for (default: from the command name) |
| `--profile NAME` | use a named profile, no picker |
| `--select A,B` | use exactly these servers, no picker |
| `-y`, `--last` | reuse the saved selection, no picker |
| `--all`, `--none` | select everything (except servers disabled in Claude Code) or nothing |
| `--home DIR` | where mcpick keeps its files (default: `$MCPICK_HOME`, else `~/.mcpick`) |
| `--addr HOST:PORT` | `serve` over HTTP on a loopback address instead of stdio |
| `--timeout DUR` | per-server connect timeout (default: 15s) |
| `--redact` | `import`: rewrite secrets as `${VAR}` references |
| `--json` | machine-readable `list`, `measure`, `doctor`, `targets` |

Options go before the command; everything after `run` belongs to the agent.
Only `run` opens the picker, and only on a terminal; every other command, and
`run` without a terminal, uses the saved selection.

Exit status: the agent's own; 128+n when it died of signal n; 130 when the
picker was aborted; 1 for mcpick's errors, or an unreachable server under
`doctor`.

| Environment | |
|---|---|
| `MCPICK_HOME` | mcpick's home, as `--home` |
| `MCPICK_DEBUG=1` | print the exact command, with real paths, at launch |
| `MCPICK_SERVE_ALLOW_REMOTE=1` | let `serve --addr` bind a non-loopback address |
| `CLAUDE_CONFIG_DIR` | where Claude Code's config lives |

## The picker

| Key | |
|---|---|
| `↑` `↓` `j` `k`, `g` `G`, `PgUp` `PgDn` | move |
| `space` | check / uncheck |
| `/` | filter by name or address |
| `m` | measure every server (again) |
| `a`, `n` | check all (not servers disabled in Claude Code), none |
| `s`, `l` | save the selection as a profile, load one |
| `+`, `d` | add a server to the catalog, delete the highlighted one |
| `enter`, `q` | launch, abort |

The header shows mcpick's version, the agent, and under it the command that
will run. The detail line at the bottom explains the server under the cursor:
why its measurement failed, or where its credentials came from.

`d` removes a `workspace` server from the catalog after `[y/N]`, and a `project`
or `global` one from `~/.claude.json` after you type `yes` — that file is shared
by every Claude session on the machine. mcpick locks it, backs it up first
(`~/.claude.json.mcpick-bak-<time>`, five kept) and leaves the rest of it byte
for byte as it was. A Claude session running elsewhere rewrites the file when it
exits and can undo the deletion. Plugin servers cannot be deleted from mcpick.

## Where servers come from

`<root>` is the nearest directory, from the current one upwards, holding
`.mcp.yaml`, `.mcp.json` or `.git`.

| Order | Source | Shown as |
|---|---|---|
| 1 | `<root>/.mcp.yaml`, then `<root>/.mcp.json` | `workspace` |
| 2 | `~/.claude.json` → `projects[<root>].mcpServers` | `project` |
| 3 | `~/.claude.json` → `mcpServers` | `global` |
| 4 | enabled Claude Code plugins' `.mcp.json` | `plugin`, read-only |

The first definition of a name wins; a duplicate is reported. Plugins are
included because `--strict-mcp-config` drops their servers along with
everything else. New servers added with `+` go to `.mcp.yaml` when it exists.

## Catalog format

The same shape as Claude's `mcpServers` (`servers:` or `mcpServers:`), in
YAML or JSON, plus `profiles:`. See [examples/.mcp.yaml](../examples/.mcp.yaml).

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

Each agent gets its own dialect — URL keys, header tables, TOML for Codex and
Grok, command arrays for opencode — and keys only one agent understands
(`bearer_token_env_var`, `includeTools`, ...) reach only that agent.

## Measuring

`m`, `measure` and `doctor` run the MCP handshake, list each server's tools and
estimate what their definitions cost in context: four bytes per token over the
serialised tool schemas. It is an estimate, close enough to see which server
takes a fifth of the window.

The picker starts with nothing measured; `m` measures every server again each
time. Servers from the project's catalog are measured only once checked:
measuring runs their command or contacts their host, with your environment
expanded into it.

Failures are reduced to a word in the list — `401`, `403`, `refused`, `dns`,
`timeout`, `tls`, `no cmd`, `exited`, `env var`, `expired` — with the full
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

```yaml
profiles:
  review: [github, sentry]
```

Profiles live in the catalog, so they travel with the project. `s` and `l` in
the picker, or `mcpick profile`. A profile naming a server that is no longer in
the catalog is reported.

## Files

```
~/.mcpick/                       --home DIR  >  $MCPICK_HOME  >  ~/.mcpick
├── selections/<workspace>-<hash>/<uid>.json   what you picked, per project and --uid
├── state/                                     tokens.json, measurements.json, restore/
└── run/                                       rendered configs and overlays
```

The home is `0700` and its files `0600`. Configs in `run/` hold expanded
secrets; they are named after the host and process that own them, and removed
once that process has ended. `~/.mcpick` can be shared between containers: a
launch never removes a file belonging to a process on another host until it is
clearly stale. Selections saved by older versions (`<root>/.tmp/`,
`<root>/.scratchpad/mcpick/`, `$XDG_STATE_HOME/mcpick/`) are read until the
next save, never modified.

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
after 30 seconds. Only tools are aggregated, not resources or prompts.

The proxy has no authentication of its own, so `--addr` accepts only loopback
addresses and requests with a non-local `Origin` are refused.
`MCPICK_SERVE_ALLOW_REMOTE=1` lifts the first rule; use it only behind your
own authentication.

## Code layout

```
main.go             hands off to internal/cli
internal/cli        argument parsing, one function per command
internal/catalog    .mcp.yaml/.mcp.json, ~/.claude.json and plugins
internal/spec       the server entry and every agent's dialect
internal/target     the ten agents: flag, overlay and project-file launches
internal/mcp        MCP client (stdio, streamable HTTP, legacy SSE), measuring
internal/proxy      mcpick serve
internal/oauth      OAuth, mcpick's tokens and Claude Code's
internal/state      the saved selection
internal/tui        the picker
internal/fsutil     atomic writes, the home directory, locks
internal/proc       exec, signals, process groups
```
