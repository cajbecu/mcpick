# Use cases

Concrete setups, what mcpick does in each, and the command lines. Every
option mentioned is described in [reference.md](reference.md).

## Several agents or containers on one project

You have the same checkout open in several places at once: a few containers
sharing a bind mount, or two or three agents in one box working on one
repository. Each one should load its own servers, and none should undo
another's choice.

mcpick keeps the selection per workspace *and* per `--uid`. The uid defaults to
the hostname, which inside a container is unique per box, so containers get
separate selections without any flag. Agents sharing one box name their own:

```sh
mcpick run claude                      # uid = hostname
mcpick --uid reviewer run codex        # its own saved selection
mcpick --uid worker-2 -y run claude    # reuses worker-2's last selection
```

The uid also fills `{UUID}` in the catalog, so a server can be given one
upstream session per box:

```yaml
servers:
  browser:
    type: http
    url: http://127.0.0.1:9010/mcp?session={UUID}
```

`~/.mcpick` can be mounted into every container (`-v ~/.mcpick:/root/.mcpick`).
Rendered configs are named after the host and process that own them, and a
launch never removes a file that belongs to a process on another host until it
is clearly stale. For agents mcpick reaches by rewriting a project file
(gemini, grok, devin) a second session on the same file is refused
rather than allowed to corrupt the first one's restore, and restore records
carry the host that wrote them; see [agents.md](agents.md).

## Cloud, remote and sandboxed boxes

mcpick often runs where there is no browser and no desktop: a VM reached over
ssh, a devcontainer, an agent sandbox. Nothing in it needs one.

`mcpick login <server>` prints the authorization URL instead of opening it:

```sh
mcpick login github
# Open this URL to authorize mcpick:
#
#   https://auth.example.com/authorize?...&redirect_uri=http%3A%2F%2F127.0.0.1%3A41217%2Fcallback...
```

The callback lands on the box's own loopback, at the port in that
`redirect_uri`. From another machine, forward it before opening the URL
(`ssh -L 41217:127.0.0.1:41217 box`; the flow waits five minutes), or run the
login on your laptop against the same catalog and copy
`~/.mcpick/state/tokens.json` to the box — tokens are keyed by server name and
URL, so they apply wherever the catalog names the same server. Once stored,
tokens are attached when measuring, launching and serving, and refreshed
before they expire.

Rendered configs hold expanded secrets. They are `0600` files in a `0700`
home, but they are on disk. On a box you do not fully trust, point the home at
a tmpfs so they never are:

```sh
export MCPICK_HOME=/dev/shm/mcpick     # Linux; also holds selections and tokens
mcpick --profile review run claude
```

Everything mcpick writes goes there — selections and tokens included, so they
are gone after a reboot. If Claude Code's config lives somewhere other than
`~/.claude`, mcpick follows `CLAUDE_CONFIG_DIR` the same way Claude does. On a
slow link, raise `--timeout` (default `15s`) so a measurement is a measurement
and not a timeout.

Without a terminal — a command run through an agent, a `docker exec` without
`-t` — `run` opens no picker and uses the saved selection. Say what you want
explicitly instead:

```sh
mcpick --select github,sentry run claude
```

## CI and scripts

Only `run` opens the picker, and only on a terminal; every other command, and
`run` without one, uses the saved selection. In a script, name the selection
rather than rely on what a previous run saved — `-y` on a fresh box means "no
servers" (mcpick warns on stderr, then launches):

```sh
mcpick --select github,sentry run claude -p "review the diff"
mcpick --profile ci run codex exec "run the tests"
```

`doctor` is a health check: it connects to the selected servers and exits `1`
when any is unreachable or skipped (a stdio command not yet trusted; see
`--trust`), or when nothing is selected.

```sh
mcpick --profile ci --timeout 5s doctor            # human-readable
mcpick --profile ci --timeout 5s --json doctor     # one object per server: name, ok, error, tools, tokens, ms, remote, auth, skipped
```

`--json` also applies to `list`, `measure`, `agents` and `profile list`.
`measure` reports failures in its output but exits `0`; use `doctor` when the
exit status is what matters.

Exit status: the agent's own; `128+n` when it died of signal `n`; `130` when
the picker was aborted; `1` for mcpick's own errors and for `doctor` with an
unreachable or skipped server.

A profile pins the set once. One that travels with the repository is a
`profiles:` key in `.mcp.yaml`, written by hand, so the script and the
catalog cannot drift apart; one of your own, valid in every project, is saved
from the command line:

```sh
mcpick --select github,sentry profile save ci      # ~/.mcpick/profiles.yaml
```

## Trimming context cost per task

Every server an agent loads costs its tool definitions on every request; a big
one costs more than a long prompt. mcpick shows the cost so the choice is made
with numbers.

In the picker, `m` measures every server and the header keeps a running total
for what is checked. Outside it:

```sh
mcpick measure                         # every server, most expensive first
```

The estimate is four bytes per token over the serialised tool schemas — close
enough to see which server takes a fifth of the window.

## A personal profile per task

Then keep one selection per kind of work. Your own profiles live in
`~/.mcpick/profiles.yaml` and are valid in every project: `p` in the picker
opens the profile manager, `+` saves the current selection under a name,
`space` in the right pane adds or removes a server, and `enter` on a profile
launches with it. From the command line:

```sh
mcpick --select github,sentry profile save review
mcpick --select playwright profile save browser
mcpick --profile review run claude
mcpick --profile browser run codex
mcpick --none run claude               # no MCP servers at all
```

A profile the whole team should have goes in the catalog instead, as a
`profiles:` key in `.mcp.yaml`. Catalog profiles are shared: mcpick lists
and loads them, `c` copies one into yours, `d` deletes it from the file.
`default` always exists and is everything available — the same as `--all`,
so it skips what you hid.

## Hiding servers you never use here

A catalog, and `~/.claude.json` behind it, accumulates servers that make
sense somewhere else: a project's own tools in another checkout, a plugin
you tried once. They cost nothing while unchecked, but they are in the way
on every launch.

`h` on a row hides it: it leaves its group for a **Hidden (N)** section at
the bottom, folded until `H` unfolds it. `a` and `--all` leave hidden
servers unchecked, `m` does not measure them while folded, and the built-in
`default` profile skips them. Hiding is not unchecking: a hidden server you
had checked still loads, and the header says `N hidden checked` until you
uncheck it, so nothing disappears silently. The set is yours and per
project, in `~/.mcpick/state/hidden/`, never in the catalog:

```sh
mcpick run claude                      # h on the rows you never use here, H to see them again
mcpick --all run codex                 # everything but what you hid
```

## One catalog for many agents

The catalog is one file, `.mcp.yaml`, in Claude's `mcpServers` shape. Each
agent gets it rendered in its own dialect — JSON with the keys it understands,
TOML for Codex and Grok, command arrays for opencode — so the same file serves
every agent you launch:

```sh
mcpick run claude
mcpick run codex
mcpick run gemini
mcpick agents                          # every agent mcpick can launch, and how
```

To start from what Claude Code already has:

```sh
mcpick import                          # ~/.claude.json → .mcp.yaml, secrets as ${VAR:?export VAR}
```

`export` prints the selection in an agent's dialect, for a tool mcpick does not
launch or a config you keep by hand:

```sh
mcpick --profile review --agent codex export       # TOML, [mcp_servers.<name>]
mcpick --profile review export                     # claude's JSON, the default
```

Some agents also load MCP config from files no launcher can switch off
(grok, and codex/copilot/pi/opencode when the project has its own MCP
file). mcpick names those servers before launching; [agents.md](agents.md)
has the list per agent, and the agents it does not support (the
Antigravity CLI is launched unchanged).

## `mcpick serve` as one stable endpoint

Some clients are configured once and not from a command line: an IDE, an
agent mcpick has no adapter for. Instead of editing their config each time
the selection changes, point them at mcpick once. It runs as one MCP server in
front of the selection, with tools named `<server>__<tool>`:

```sh
mcpick --profile review serve                          # stdio
mcpick --profile review serve --addr 127.0.0.1:7000    # streamable HTTP
```

As a stdio server in a client's config, give it the catalog explicitly, since
the client will not run it from the project directory:

```json
{
  "mcpServers": {
    "mcpick": {
      "command": "mcpick",
      "args": ["--file", "/home/me/src/app/.mcp.yaml", "--profile", "review", "serve"]
    }
  }
}
```

Changing the profile in the catalog changes what the client sees; its own
config never moves again. An upstream that is down hides its tools rather than
breaking the list and is retried after 30 seconds. Only tools are aggregated,
not resources or prompts.

The proxy has no authentication of its own and forwards every call with the
upstreams' credentials, so `--addr` accepts loopback addresses only.
`MCPICK_SERVE_ALLOW_REMOTE=1` lifts that; use it only behind your own
authentication.

## Opening an untrusted repository

A cloned repository can carry its own `.mcp.yaml`, and a server definition is
a command to run or a host to contact, with your environment expanded into it.
mcpick treats a project's servers as untrusted until you choose them:

```sh
mcpick list                            # what the repository defines, without connecting to anything
```

Measuring a stdio server runs its command, so `m` in the picker runs no
command you have not reviewed: such a row says `run?`, `m m` or `M` shows
the whole command and asks, and `y` runs and remembers it per project. A
remote server the repository defines is measured unticked only when nothing
of yours would leave; otherwise its row says `ask` and it is contacted once
ticked, or once `T` has trusted the repo — the catalog as a whole, keyed by
its contents. The rules, in full, are in [SECURITY.md](../SECURITY.md).
`mcpick measure` applies the same ones and says so:

```
mcpick: not measured, their commands are not trusted: local-tool (uvx some-mcp); re-run with --trust to run them and remember that
mcpick: not measured, defined by this repository and not checked: stripe (sends ${STRIPE_API_KEY} to its host) (name them with --select or tick them in the picker, or --trust-catalog to trust this repository's catalog; --all and a catalog profile are not a check)
```

```sh
mcpick measure                         # runs nothing you have not trusted
mcpick --trust measure                 # approves what the catalog says, printing each command first
mcpick --trust-catalog measure         # trusts the catalog's remote servers, printing what that covers
```

Launching runs exactly what you select. To open the repository with none of
its servers, or only with your own:

```sh
mcpick --none run claude
mcpick --select github run claude      # a server from ~/.claude.json, not the repository
```

Before selecting a repository server, read its entry: `${VAR}` references in
its URL, headers or arguments are filled from your environment. The first run
in a project checks only your own servers (local, user, plugin), never the
repository's. Once a server is selected it is not sandboxed —
[SECURITY.md](../SECURITY.md) says what mcpick does and does not do.

## Wrapper scripts

When the agent is started through a script — `my-claude` that sets a proxy
and calls `claude`, a `claude` alias with default flags — the command name no
longer says which agent it is. `--agent` says it instead:

```sh
mcpick --agent claude run my-claude --model opus
```

For claude the flags go right after the command, before anything you passed
(`my-claude --mcp-config <rendered> --strict-mcp-config --model opus`), so a
wrapper that runs `claude "$@"` needs no change; agents reached through an
environment variable or a project file need even less. To see the exact command
with real paths at launch:

```sh
MCPICK_DEBUG=1 mcpick --agent claude run my-claude
```

A command mcpick has no adapter for runs unchanged, with `MCPICK_CONFIG`
pointing at a Claude-shaped config the wrapper can read itself.
