# mcpick

**Choose which MCP servers an AI coding agent loads — at the moment you launch
it, with what each one costs in context in front of you.**

Agents connect to every MCP server they can find and pay for all of their tool
definitions on every request. One server can take 25k tokens of context whether
the session needs it or not. mcpick puts a checkbox list in front of the
agent, launches it with only what you checked, and leaves your config files
as they were.

```
mcpick 0.1.0 uid=laptop · 3/7 selected · ~12.1k context  ✻ claude
→ claude --mcp-config <rendered config> --strict-mcp-config

Workspace • 1/2 selected  ~/src/app/.mcp.yaml
  [x] playwright           4.4k  http://localhost:8931/mcp
  [ ] local-tool                 uvx some-mcp

Global • 2/4 selected  ~/.claude.json
  [ ] ahrefs              25.6k  https://api.ahrefs.com/mcp/mcp
  [x] google-webmaster     2.3k  https://mcp.example.com/gsc
  [x] tastytrade           5.4k  http://localhost:8000/mcp
  [ ] claude_design              disabled in Claude Code  https://api.anthropic.com/v1/design/mcp

Legend: 401 check failed · 4.2k context cost · [x] loads · ··· measuring
↑↓ move · space toggle · / filter · m measure · a all · n none · s save · l load · enter launch · q abort
```

## Install

```sh
brew install cajbecu/tap/mcpick                # macOS and Linux
go install github.com/cajbecu/mcpick@latest    # anywhere Go runs
```

[Releases](https://github.com/cajbecu/mcpick/releases) also have archives for
Linux, macOS (one universal binary) and Windows, `.deb`/`.rpm`/`.apk`
packages, and there is a Nix flake: `nix run github:cajbecu/mcpick`.

## Use

```sh
mcpick run claude                      # pick, then launch
```

In the picker: `space` checks a server, `m` measures what each one costs,
`enter` launches. The line under the header is the exact command that will run.

```sh
mcpick -y run claude                   # skip the picker, reuse the last selection
mcpick --select github,sentry run codex
mcpick --profile review run gemini     # a named selection from the catalog
mcpick measure                         # context cost of every server
mcpick doctor                          # can the selected servers be reached?
```

Everything after `run` belongs to the agent; mcpick never reads those flags.

## Agents

mcpick recognises the agent from the command name, or from `--target`:

| Agent | Selection is exclusive? |
|---|---|
| claude, gemini | yes |
| codex, copilot, pi, opencode | yes, unless the project has its own MCP file |
| muse, devin | yes, as far as documented |
| antigravity, grok | no: they also load MCP config mcpick cannot switch off |

When an agent will load servers you did not pick, mcpick says so before
launching. Each agent is reached in the least invasive way it allows — a flag,
a redirected config directory, or a project file rewritten for the run and put
back after. [docs/agents.md](docs/agents.md) explains how, per agent.

A server you disabled in Claude Code shows `disabled in Claude Code`. Checking
it re-enables it in Claude Code when you launch.

## Your servers

mcpick reads, in this order (first definition of a name wins):

1. `.mcp.yaml` or `.mcp.json` in the project — your catalog, which mcpick can edit
2. `~/.claude.json` — the project's servers, then your global ones
3. servers of your enabled Claude Code plugins

A catalog looks like Claude's `mcpServers`, in YAML, and can take secrets from
the environment:

```yaml
servers:
  ahrefs:
    type: http
    url: https://api.ahrefs.com/mcp/mcp
    headers:
      Authorization: "Bearer ${AHREFS_TOKEN:?export AHREFS_TOKEN first}"
  local-tool:
    type: stdio
    command: uvx
    args: [some-mcp]

profiles:
  review: [ahrefs, local-tool]
```

`mcpick import --redact` copies your servers from `~/.claude.json` into one,
with credentials turned into `${VAR}` references.

## Measuring

`m` in the picker, or `mcpick measure`, connects to each server, lists its
tools and estimates what their definitions cost in context. It uses the tokens
Claude Code already holds for a server, so a server Claude is signed in to shows
its cost rather than `401`. When a server fails, its row says why in a word
(`401`, `refused`, `timeout`, ...); move onto it for the full message.

Servers defined by the project's own catalog are measured only once you have
checked them: measuring runs their command or contacts their host, and a
cloned repository should not get to do that on a keypress.

## Where mcpick keeps things

Everything mcpick writes is in `~/.mcpick` (or `--home` / `MCPICK_HOME`):
your selections, OAuth tokens, and the configs it renders for a launch, which
are private to you and removed when the session ends. It writes nothing into
your project.

## More

- [docs/agents.md](docs/agents.md) — how each agent is launched, and what mcpick changes
- [docs/reference.md](docs/reference.md) — commands, flags, catalog format, files, OAuth, `serve`
- [SECURITY.md](SECURITY.md) — what happens to your credentials
- [CONTRIBUTING.md](CONTRIBUTING.md) — adding an agent is the most wanted contribution
- `mcpick --help`, `man mcpick`

## License

MIT
