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

**`mcpick import` copies specs verbatim** from `~/.claude.json`, live
credentials included, into a file that usually sits in a git repository. Use
`--redact` to rewrite them as `${VAR}` references; mcpick warns when you do not.

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

**Overlay directories are symlinks into your real config directory.** When
mcpick redirects `CODEX_HOME` or `XDG_CONFIG_HOME`, the agent still reads your
actual credential files through those links. The overlay is not a sandbox and is
not meant to be one — it exists so redirecting the config directory does not log
you out.

**`mcpick serve` does not authenticate.** With `--addr` it listens on plain
HTTP and forwards every call to the upstream servers with their credentials.
It therefore refuses any address that is not loopback, unless
`MCPICK_SERVE_ALLOW_REMOTE=1` is set, and it rejects HTTP requests whose
`Origin` is not local, as the MCP specification requires, so a web page cannot
drive it through DNS rebinding. The stdio transport, which is the default, has
no such exposure.

**A project's own catalog is not trusted until you choose its servers.**
Measuring a server runs its command or contacts its host, with your environment
expanded into the URL and headers. mcpick therefore measures the servers a
project's `.mcp.yaml` / `.mcp.json` defines only once you have selected them —
the same consent a launch needs — so a cloned repository cannot run a command
or send a secret elsewhere because you pressed `m`.

**MCP servers are not sandboxed by mcpick.** Selecting fewer of them is a real
reduction in what a prompt injection can reach, which is part of the point, but
mcpick does not inspect, filter or contain what a server does once it is
selected.
