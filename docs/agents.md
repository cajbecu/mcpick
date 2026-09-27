# How mcpick launches each agent

Agents differ in what they let you override for a single run. mcpick uses the
least invasive mechanism each one offers. `mcpick agents` prints the same
information, with the documentation each adapter was written against.

| Agent | How mcpick reaches it | Your files | Selection is exclusive? |
|---|---|---|---|
| **claude** | `--mcp-config` + `--strict-mcp-config` | untouched | yes |
| **gemini** | `.gemini/settings.json` + `--allowed-mcp-server-names` | rewritten, restored on exit | yes |
| **codex** | overlay behind `CODEX_HOME` | untouched | unless `.codex/config.toml` has servers |
| **copilot** | overlay behind `COPILOT_HOME` | untouched | unless `.mcp.json` / `.github/mcp.json` have servers |
| **pi** | overlay behind `PI_CODING_AGENT_DIR` | untouched | unless the adapter's shared files have servers |
| **opencode** | overlay behind `XDG_CONFIG_HOME` | untouched | unless `./opencode.json` has servers |
| **muse** | overlay behind `XDG_CONFIG_HOME` | untouched | yes, as far as documented |
| **grok** | `.grok/config.toml` | rewritten, restored on exit | no: it also reads `~/.grok/config.toml`, `~/.claude.json`, `.mcp.json`, `.cursor/mcp.json` |
| **devin** (CLI) | `.devin/mcp_config.json` | rewritten, restored on exit | yes, as far as documented |

The command name picks the agent. A wrapper script gets the right treatment
with `--agent`: `mcpick --agent claude run my-claude-wrapper`. Anything else
runs unchanged, with `MCPICK_CONFIG` pointing at a Claude-shaped config; the
picker shows it as `? <command> (unknown agent)`.

The line under the picker's header is the command that will run. Set
`MCPICK_DEBUG=1` to have the real one, with real paths, printed at launch.

## When the selection is not exclusive

Some agents merge in MCP config from files no launcher can switch off. Before
every launch mcpick reads those files and names the servers that will load
anyway:

```
mcpick: grok also loads github, sentry from .mcp.json; mcpick cannot switch those off
```

## Overlays (codex, copilot, pi, muse, opencode)

These agents let an environment variable move their whole config directory.
Pointing it at an empty directory would log you out, so mcpick builds an
overlay instead: a shadow of the real directory in which every entry is a
symlink back to it, except the one config file mcpick generates.

Whatever the agent writes during the session — a login refreshed by atomic
rename, a history file, a trusted-project entry in its own config — is carried
back into the real directory when it exits, with only the server block reset
to yours. Overlays need symlinks, which on Windows means Developer Mode.

`XDG_CONFIG_HOME` (muse, opencode) is redirected for the whole agent process,
not just the agent's own directory; every other entry in it is a symlink, so
tools the agent runs still find their config.

## Project files (gemini, grok, devin)

These agents offer no per-run override. mcpick writes the project's config
file, keeps running while the agent does, and puts the file back when it
exits:

- the rest of the file — every setting that is not a server — is kept;
- settings you changed from inside the agent during the run are kept, and only
  the servers are restored;
- a file mcpick had to create is removed again, with any directory it created;
- a second session on the same file is refused rather than allowed to corrupt
  the first one's restore;
- a session killed hard is recovered on the next launch, or with
  `mcpick restore`. Restore records carry the host they were written on, so a
  container sharing the project never "recovers" a file another container is
  using.

## Servers disabled in Claude Code

Claude Code keeps a list of servers you switched off (`disabledMcpServers` in
`~/.claude.json`), and honours it even for servers passed with `--mcp-config`
(anthropics/claude-code#14490). In the picker such a server says so, and
checking it says what launching will do:

```
[ ] notion          disabled in Claude Code           nothing changes; it does not load
[x] notion          will be enabled in Claude Code    re-enabled in Claude Code at launch, and loads
```

Re-enabling is always an explicit choice: the picker opens with these servers
unchecked even when an earlier run saved them as selected, and neither `a` in
the picker nor `--all` checks them. Once re-enabled, a server is an ordinary one
in every session.

Without the picker — `--select`, `--profile`, `-y` — mcpick never changes
Claude Code's settings. A selected disabled server then runs for that session
only, handed to Claude as `<name>_mcpick`, a name the disabled list does not
contain. Two consequences: permission rules written for `mcp__<name>__*` do not
match its tools, and an OAuth server has to sign in again, because Claude keeps
tokens by name.

A plugin's server is disabled under its namespaced name,
`plugin:<plugin>:<server>`; mcpick hands it over under its plain name, so it
loads without an alias.

All of this applies to `claude` only. Other agents do not read Claude's
settings, so for them a server disabled in Claude Code is an ordinary one.

## Not supported (CLI)

**Antigravity** (`agy`, installed by `antigravity.google/cli/install.sh`)
reads MCP servers from `~/.gemini/config/mcp_config.json` and from plugins
only. `mcpick run agy` launches the CLI unchanged, with one line on stderr
saying so; nothing is written into the project. Rewriting the global file
is not done: it would reach every workspace, well beyond one project's
launch. The picker's command line and `mcpick agents` say the same.

The Antigravity IDE reads `.agents/mcp_config.json` in the workspace
(`serverUrl` for remote servers). mcpick does not launch the IDE, but it
writes that shape: `mcpick --agent antigravity --profile review export >
.agents/mcp_config.json` fills the file from the catalog, credentials
expanded — keep it out of git.

**Devin's cloud product** keeps its MCP servers server-side, configured
through a web UI; there is no launch to put a picker in front of, and a cloud
agent cannot reach `mcpick serve` on your machine.

## Checked end to end

`just e2e` (see CONTRIBUTING.md) launches each agent inside a container
against fake MCP servers and checks that it loads exactly the selection:
from `codex`, `copilot`, `grok`, `devin`, `gemini` and `opencode`'s own
`mcp list`, and from what the fakes saw connect for `claude`, `pi`, `muse`,
`opencode` and `gemini`. `agy mcp list` is run too, and checked only for
what mcpick promises: launched unchanged, exit 0, the notice on stderr,
nothing left in the project; the run records it as "not supported".
Claude's `mcp list` ignores `--mcp-config` (anthropics/claude-code#15388); a
headless `claude -p` connects to every configured server before it asks for
a login, which is what the run relies on.
