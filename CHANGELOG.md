# Changelog

All notable changes to this project are documented here.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and the project uses [semantic versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/cajbecu/mcpick/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/cajbecu/mcpick/releases/tag/v0.1.0
