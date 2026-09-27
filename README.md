# mcpick

[![ci](https://github.com/cajbecu/mcpick/actions/workflows/ci.yml/badge.svg)](https://github.com/cajbecu/mcpick/actions/workflows/ci.yml) [![release](https://img.shields.io/github/v/release/cajbecu/mcpick)](https://github.com/cajbecu/mcpick/releases/latest) [![Go Reference](https://pkg.go.dev/badge/github.com/cajbecu/mcpick.svg)](https://pkg.go.dev/github.com/cajbecu/mcpick) [![license](https://img.shields.io/github/license/cajbecu/mcpick)](LICENSE)

**Choose which MCP servers an AI coding agent loads — at the moment you launch
it, with what each one costs in context in front of you.**

Agents connect to every MCP server they can find and pay for all of their tool
definitions on every request. One server can take 25k tokens of context whether
the session needs it or not. mcpick puts a checkbox list in front of the
agent and launches it with only what you checked.

```
 ✻ claude   3/7 selected · ~12.1k context · mcpick 0.2.0
→ claude --mcp-config <rendered config> --strict-mcp-config

Project • 1/3 selected  ~/src/app/.mcp.yaml
> [x] playwright    4.4k  http://localhost:8931/mcp
  [ ] stripe         ask  https://mcp.stripe.com
  [ ] local-tool    run?  uvx some-mcp

User • 2/4 selected  ~/.claude.json
  [ ] github       25.6k  https://api.githubcopilot.com/mcp/
  [x] linear        2.3k  https://mcp.linear.app/mcp
  [x] context7      5.4k  https://mcp.context7.com/mcp
  [ ] notion              disabled in Claude Code  https://mcp.notion.com/mcp

Legend: 4.2k context cost · [x] loads · run? runs a command: m m · ask sends your env: tick it or T
↑↓ move · space toggle · / filter · m measure · M review commands · a all · n none · h hide
H hidden · p profiles · + add · d delete · v move · T trust repo · enter launch · q abort
measured · skipped 2: 1 command(s) to review (m m, or M for all), 1 repository remote(s) to tick (o…
```

(A 100-column terminal after `m`. `ask` is a repository server that would send
`${STRIPE_API_KEY}` to its host, so it waits for a tick or `T`; `run?` is a
command waiting for `m m`.)

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
mcpick -y run claude                   # skip the picker, reuse the last selection
mcpick --select github,sentry run codex
mcpick --profile review run gemini     # a named selection: yours, or the catalog's
mcpick measure                         # context cost of every server
mcpick doctor                          # can the selected servers be reached?
```

The first run starts from what the agent loads today; mcpick remembers your
choice per project (and per `--uid`). In the picker: `space` checks a server,
`m` measures what each one costs, `h` hides one you never use here, `v` moves
one to another group, `p` manages profiles, `enter` launches. The line under
the header is the exact command that will run. Everything after `run` belongs
to the agent.

## Agents

mcpick recognises the agent from the command name, or from `--agent`:

| Agent | Selection is exclusive? |
|---|---|
| claude, gemini | yes |
| codex, copilot, pi, opencode | yes, unless the project has its own MCP file |
| muse, devin | yes, as far as documented |
| grok | no: it also loads MCP config mcpick cannot switch off |

Not supported (CLI): antigravity — `agy` reads its global config only, so it is
launched unchanged, with a notice. When an agent will load servers you did not
pick, mcpick says so before launching. [docs/agents.md](docs/agents.md)
explains how each agent is reached, and what launching writes: nothing for
most; a project file for gemini, grok and devin, put back when they exit.

## Your catalog

mcpick merges `.mcp.yaml` or `.mcp.json` in the project (the catalog, which
`+`, `d` and `v` edit), then `~/.claude.json`'s servers for this directory and
your own, then your enabled Claude Code plugins — groups named as Claude Code
names them: `project`, `local`, `user`, `plugin`. A catalog is Claude's
`mcpServers` shape in YAML, with `${VAR:?export VAR}` for credentials and an
optional `profiles:` key ([example](examples/.mcp.yaml)). Your own profiles
live in `~/.mcpick/profiles.yaml`; catalog profiles are shared: mcpick lists
and loads them, `c` copies one into yours, `d` deletes it from the file.
`mcpick import` seeds the catalog from `~/.claude.json`, credentials redacted;
`mcpick move` moves one server between the groups.

## Measuring

`m` in the picker, or `mcpick measure`, connects to each server, lists its
tools and estimates what their definitions cost in context, using the tokens
Claude Code already holds for a server. A failure shows as a word in the list
(`401`, `refused`, `timeout`, ...) with the full message on the detail line.

## Trust

Measuring a stdio server runs its command, and a cloned repository must not
get to do that on a keypress: `m` runs no command you have not reviewed (`run?`;
`m m` or `M` shows it whole and asks), and a repository's remote server is
contacted unticked only when nothing of yours would leave (`ask` otherwise).
`T` trusts the repository's catalog, keyed by its contents. What is written
where, and every rule, is in [SECURITY.md](SECURITY.md). Everything mcpick
keeps — selections, profiles, hidden servers, trust, tokens, rendered configs
— lives in `~/.mcpick`.

## More

- [docs/reference.md](docs/reference.md) — commands, options, environment, the picker's keys, the catalog format, files, OAuth, `serve`
- [docs/agents.md](docs/agents.md) — how each agent is launched, and what mcpick changes
- [docs/use-cases.md](docs/use-cases.md) — concrete setups, with the command lines
- [SECURITY.md](SECURITY.md) — what happens to your credentials, and the trust rules
- [docs/decisions.md](docs/decisions.md) — why mcpick works the way it does
- [CONTRIBUTING.md](CONTRIBUTING.md) — adding an agent is the most wanted contribution
- `mcpick --help`, `man mcpick`

## License

MIT
