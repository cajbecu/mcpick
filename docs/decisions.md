# Decisions

This file records the decisions that shaped mcpick: what was decided, why, and
what was turned down. Each entry is short. The code is the final authority; this
file explains why the code looks the way it does.

Numbers are chronological: each decision takes the next number when it is
made. The sections below group the entries by area, so within a section the
numbers interleave. To add a decision, append a new entry with the next number
to its area's section and add a row to the index. Never rewrite an old entry.
When a decision is replaced, set its status to "superseded by #N" and leave the
rest of it as it was.

"Decided by" says who made the call. **owner** is the project owner.
**maintainer** is the agent that maintains the code, deciding on the owner's
behalf. Dates marked "(approximate)" come from notes, not from a commit.

## Index

| # | Title | Area | Status |
|---|---|---|---|
| 1 | Pick servers at launch, render a config per session | architecture | accepted |
| 2 | Three ways to hand a selection to an agent | architecture | accepted |
| 3 | A backend registry with a conformance test | architecture | accepted; `--backend` superseded by #63 |
| 4 | A hand-written MCP client, no SDK | architecture | accepted |
| 5 | Module path and package layout | architecture | accepted |
| 6 | A rewritten project file belongs to one session | architecture | accepted |
| 7 | Agent subcommands get no session flags | architecture | accepted |
| 8 | Catalog in `.mcp.yaml` or `.mcp.json` | files | accepted |
| 9 | Selections under `<root>/.tmp` | files | superseded by #10 |
| 10 | Everything mcpick writes lives in `~/.mcpick` | files | accepted; the legacy `.tmp` read superseded by #70 |
| 11 | Hidden set next to the selections | files | superseded by #12 |
| 12 | The hidden set is personal and per project | files | accepted |
| 13 | A read-only `config.yaml` | files | accepted |
| 14 | Profiles written into the catalog | files | superseded by #15 |
| 15 | Profiles are personal, in `profiles.yaml` | files | accepted |
| 16 | Catalog profiles are read-only | files | superseded by #17 |
| 17 | A catalog profile can be deleted from its file | files | accepted |
| 18 | Writes go to the server's own file, with one backup each | files | accepted |
| 19 | Tests cannot touch the real `~/.claude.json` | files | accepted |
| 20 | Moving a server between project, local and user | files | accepted |
| 21 | No stdio command is trusted by default | security | accepted |
| 22 | Trust fingerprint on env keys | security | superseded by #23 |
| 23 | Trust fingerprint on the command as written | security | accepted |
| 24 | `m m` trusts and runs at once | security | superseded by #25 |
| 25 | `m m` opens a consent screen | security | accepted |
| 26 | Credentials masked on the consent screen | security | superseded by #27 |
| 27 | Consent shows everything; other output masks | security | superseded by #60 |
| 28 | Repository remotes measured only once checked | security | superseded by #29 |
| 29 | Harmless repository remotes measured unchecked, plus `T` | security | superseded by #56 |
| 30 | `{UUID}` is not an exposure | security | superseded by #31 |
| 31 | `{UUID}` keeps a remote behind the check | security | accepted |
| 32 | Public-only measurements refuse private addresses | security | accepted |
| 33 | Borrow Claude Code's OAuth tokens, never refresh them | security | accepted |
| 34 | No measurement survives a run | security | accepted |
| 35 | `serve` listens on loopback and checks Origin | security | accepted |
| 36 | End-to-end tests carry no credentials | security | accepted |
| 37 | E2E build context as a denylist | security | superseded by #38 |
| 38 | E2E build context as an allowlist | security | accepted |
| 39 | Servers disabled in Claude Code | UX | accepted |
| 40 | Unbracketed paste: the first keys act | UX | superseded by #41 |
| 41 | Command keys are held until a burst is ruled out | UX | accepted |
| 42 | Inline drawing, clipped to the window | UX | superseded by #43 |
| 43 | Draw on the alternate screen | UX | accepted |
| 44 | Filter the list in place | UX | accepted |
| 45 | Unsupported and partly supported agents are said plainly | UX | accepted; the antigravity part superseded by #64 |
| 46 | Static, reproducible release builds and OS packages | distribution | accepted |
| 47 | Homebrew cask, not formula | distribution | accepted |
| 48 | Snapshot versions from `git describe` | distribution | accepted |
| 49 | Provenance attestation only on a public repository | distribution | accepted |
| 50 | CI checks vulnerabilities, the release and the Nix build | distribution | accepted |
| 51 | Commit identity set per repository | process | accepted |
| 52 | History rewritten to one 0.1.0 commit | process | accepted |
| 53 | The maintainer decides when the owner is away | process | accepted |
| 54 | Project artifacts in English | process | accepted |
| 55 | Project files are private; Gemini expands its own env references | security | accepted; the existing file's mode superseded by #74, the reference forms by #75 |
| 56 | A check is bound to origin and fingerprint; `T` hashes what was parsed | security | accepted |
| 57 | `--all` and catalog profiles are not consent to measure | security | accepted |
| 58 | `measurements.json` keeps the kind of an error, and a masked message | security | accepted |
| 59 | `import` redacts by default | security | accepted |
| 60 | Consent shows everything; every other output masks arguments and env | security | accepted |
| 61 | Group names follow Claude Code's scopes (project/local/user) | UX | accepted |
| 62 | Consent vocabulary: `run?`, `ask`, review commands, trust repo | UX | accepted |
| 63 | `--agent` and `mcpick agents`; `--target` and `targets` kept as aliases | UX | accepted |
| 64 | The Antigravity CLI is not supported and launched unchanged | UX | accepted |
| 65 | The first run starts from what the agent loads without mcpick | UX | accepted |
| 66 | A catalog trust carries over the picker's own writes | security | accepted; which rows follow the file superseded by #69 |
| 67 | Flags after `run <cmd>` are reported; selection flags are exclusive | UX | accepted; the scan refined by #77 |
| 68 | Shadowed definitions: one line per pair of files, silent for the same server | UX | accepted |
| 69 | After a picker write, only the rows it wrote follow the file | security | accepted |
| 70 | No selection is read from inside the project | files | accepted |
| 71 | Credentials are found by their form, and around a placeholder | security | accepted |
| 72 | An edit never strands a YAML alias | files | accepted |
| 73 | Expanded values are put back as references in error text | security | accepted |
| 74 | An existing project file is private for the run | security | accepted |
| 75 | Gemini gets `${NAME}` for the set `:?` and `:-` forms | security | accepted |
| 76 | `--redact` and `--yes` together are an error | UX | accepted |
| 77 | Late-flag reports know `--`, values and the agent's own flags | UX | accepted |

## Architecture

### 1. Pick servers at launch, render a config per session

**Status:** accepted
**Area:** architecture
**Decided by:** owner
**Date:** 2026-09-16 (approximate)

**Context:** Claude Code loads every MCP server it is configured with, and each
server's tool list costs context in every session. Editing the agent's config to
switch servers on and off is slow and affects every session at once.

**Decision:** mcpick sits in front of the agent. `mcpick run <cmd>` shows a
picker, renders a config with only the chosen servers, and launches the agent on
it. For `claude` that is `--mcp-config <rendered> --strict-mcp-config`.

**Why:** the choice then applies to one session and needs no edits to the
agent's own files.

**Alternatives rejected:** editing `~/.claude.json` in place (global, and racy
with a running agent); a long-running MCP gateway only (adds a hop to every tool
call; kept as the optional `serve` instead, #35).

**Consequences:** the selection is saved per workspace and `--uid`, so the next
launch starts from it.

### 2. Three ways to hand a selection to an agent

**Status:** accepted
**Area:** architecture
**Decided by:** maintainer
**Date:** 2026-09-24 or earlier (in 0.1.0)

**Context:** agents read MCP servers from different places, and only some take
a config path on the command line.

**Decision:** each backend uses one of three mechanisms: session flags
(`claude`); an overlay, a shadow of the agent's config directory in which every
entry is a symlink back and the MCP file is replaced (`codex`, `copilot`,
`muse`, `opencode`, `pi`); or a project file rewritten for the run and restored
on exit (`gemini`, `antigravity`, `grok`, `devin`). The generic backend sets
`MCPICK_CONFIG` and leaves the rest to the command.

**Why:** each is the least invasive option the agent allows.

**Alternatives rejected:** rewriting the agent's global config (affects other
sessions and survives a crash).

**Consequences:** a project-file backend can leave the file rewritten if mcpick
is killed; `mcpick restore` puts it back (#6).

### 3. A backend registry with a conformance test

**Status:** accepted; the `--backend` flag that came with the registry is superseded by #63
**Area:** architecture
**Decided by:** owner (on the maintainer's proposal)
**Date:** 2026-09-24 or earlier (in 0.1.0)

**Context:** the list of agents keeps growing, and each backend must behave the
same way towards the rest of mcpick.

**Decision:** backends register themselves (`Register`, `All`, `Pick` in
`internal/backend/registry.go`); optional steps before launch go through the
`Preparer` interface. `TestConformance` runs every registered backend through
the same checks.

**Why:** adding an agent is one file and cannot skip the shared contract.

**Alternatives rejected:** a switch on the agent name in the CLI (every new
agent touches shared code).

### 4. A hand-written MCP client, no SDK

**Status:** accepted
**Area:** architecture
**Decided by:** maintainer
**Date:** 2026-09-16 (approximate)

**Context:** mcpick only needs a small part of MCP: initialize, list tools,
prompts and resources, over stdio and HTTP, to measure and check servers.

**Decision:** the client in `internal/mcp` is written by hand. Dependencies stay
limited to Bubble Tea v2, Lip Gloss v2, ultraviolet, `x/ansi`, `x/term`,
`x/sys` and `yaml.v3`.

**Why:** a small binary and a small supply chain for a tool that runs other
people's commands.

**Alternatives rejected:** an MCP SDK (far more surface than a client that only
lists).

**Consequences:** the protocol is not checked by a library, so the tests do it:
`strictServer` in `internal/mcp/transport_test.go` rejects an initialize
without `protocolVersion`, `capabilities` or `clientInfo`. It was added on
2026-09-23 after a mechanical rename broke the `protocolVersion` field unnoticed.

### 5. Module path and package layout

**Status:** accepted
**Area:** architecture
**Decided by:** owner (module path); maintainer (layout)
**Date:** 2026-09-23

**Context:** the prototype had to become a publishable Go module.

**Decision:** the module is `github.com/cajbecu/mcpick`. `main.go` only calls
`cli.Main`; all code is under `internal/` (backend, catalog, cli, config,
fsutil, mcp, oauth, proc, profile, proxy, spec, state, testguard, trust, tui).

**Why:** `internal/` promises no library API, so packages can change freely.

**Alternatives rejected:** `cmd/mcpick` and `src/` layouts (one binary needs
neither); exported packages (nothing outside mcpick uses them).

### 6. A rewritten project file belongs to one session

**Status:** accepted
**Area:** architecture
**Decided by:** maintainer
**Date:** 2026-09-24 or earlier (in 0.1.0)

**Context:** two sessions rewriting the same project file would each "restore"
the other's generated config as the original.

**Decision:** before rewriting, mcpick records the original and what it wrote.
A second session on the same file is refused with the pid of the first; after a
crash, `mcpick restore` puts the file back.

**Why:** the restore can tell mcpick's own content from the user's edits.

**Alternatives rejected:** last writer wins (loses the user's file).

### 7. Agent subcommands get no session flags

**Status:** accepted
**Area:** architecture
**Decided by:** maintainer
**Date:** 2026-09-25

**Context:** `claude mcp …` or `gemini extensions …` are management commands,
not sessions; session flags break them. End-to-end tests found `gemini` getting
`--allowed-mcp-server-names` on every command line.

**Decision:** `claude` subcommands and `gemini`'s `mcp`, `extensions`, `skills`,
`hooks` and `gemma` run without session flags. The same round fixed `devin`
(writes `.devin/mcp_config.json`) and `muse` (writes
`{"schema_version":1,"mcpServers":…}`).

**Why:** mcpick must never change what a management command does.

## Files

### 8. Catalog in `.mcp.yaml` or `.mcp.json`

**Status:** accepted
**Area:** files
**Decided by:** owner
**Date:** 2026-09-16 (approximate)

**Context:** the catalog lists every server a workspace can use.

**Decision:** mcpick reads `<root>/.mcp.yaml` or `<root>/.mcp.json`, the first
that exists; with neither, new entries go to `.mcp.yaml`.

**Why:** `.mcp.json` is what the ecosystem already writes; YAML takes comments,
so it is preferred for new files.

**Alternatives rejected:** a format of mcpick's own (nothing else would read it).

### 9. Selections under `<root>/.tmp`

**Status:** superseded by #10
**Area:** files
**Decided by:** maintainer
**Date:** 2026-09-23

**Context:** the saved selection needed a place.

**Decision:** 0.1.0 kept it in `<root>/.tmp/mcpick-<uid>.json`, overridable
with `--state`, `MCPICK_STATE` and `MCPICK_STATE_DIR`.

**Why:** close to the workspace, no setup.

### 10. Everything mcpick writes lives in `~/.mcpick`

**Status:** accepted; the legacy `.tmp` read superseded by #70
**Area:** files
**Decided by:** owner
**Date:** 2026-09-24

**Context:** state inside the workspace dirties the checkout and is shared by
everyone who uses it.

**Decision:** one home: `--home`, else `$MCPICK_HOME`, else `~/.mcpick`, with
`selections/`, `state/` and `run/` inside. Directories are 0700; a directory
that is a symlink or owned by another user is refused. Runtime files in `run/`
are named `<pid>.<host>.<name>`. `--state`, `MCPICK_STATE` and
`MCPICK_STATE_DIR` were removed.

**Why:** personal state stays personal, and one place is easy to find and wipe.

**Alternatives rejected:** keeping the per-workspace file (#9); XDG directories
(three places instead of one).

**Consequences:** a breaking change, accepted before the first release. The old
locations (`<root>/.tmp/mcpick-<uid>.json`,
`<root>/.scratchpad/mcpick/<uid>.json`, `$XDG_STATE_HOME/mcpick/…`) are still
read, never written; the prototype's `.scratchpad` path was dropped in 0.2.0.

### 11. Hidden set next to the selections

**Status:** superseded by #12
**Area:** files
**Decided by:** maintainer
**Date:** 2026-09-25

**Context:** `h` hides servers never used in a workspace.

**Decision:** the hidden set was saved as `selections/<workspace>/hidden.json`.

**Why:** it sat beside the selection it filters.

### 12. The hidden set is personal and per project

**Status:** accepted
**Area:** files
**Decided by:** owner (personal, per project); maintainer (location)
**Date:** 2026-09-25

**Context:** servers one person never uses in a project are not a fact about the
project.

**Decision:** the hidden set is kept in `~/.mcpick/state/hidden/<name>-<hash>.json`,
one file per workspace, shared by every `--uid`.

**Why:** hiding is a personal habit, not a selection; moving it out of
`selections/` in review kept one kind of file per directory.

**Alternatives rejected:** a list in the catalog (would impose one person's
habit on everyone); per `--uid` (hiding would have to be repeated per session).

### 13. A read-only `config.yaml`

**Status:** accepted
**Area:** files
**Decided by:** owner
**Date:** 2026-09-25

**Context:** the picker's height needed a setting.

**Decision:** `~/.mcpick/config.yaml` holds preferences, starting with
`max_rows` (default 10). mcpick never writes it. Unknown keys are ignored; a bad
value gives one warning and the default.

**Why:** a file the user owns can carry comments and cannot be clobbered.

**Alternatives rejected:** a flag only (repeated on every launch); settings
saved by the picker (mcpick would own the file).

### 14. Profiles written into the catalog

**Status:** superseded by #15
**Area:** files
**Decided by:** maintainer
**Date:** 2026-09-23

**Context:** 0.1.0 saved named selections as profiles.

**Decision:** profiles were written into the catalog file.

### 15. Profiles are personal, in `profiles.yaml`

**Status:** accepted
**Area:** files
**Decided by:** owner
**Date:** 2026-09-25

**Context:** writing profiles into the catalog changes a file the whole team
shares.

**Decision:** your profiles live in `~/.mcpick/profiles.yaml` and are managed
from the picker (`p`) or `mcpick profile`. A `default` profile always exists and
means every server (`--all`); a catalog profile named `default` is refused. A
personal profile shadows a catalog profile of the same name. The file is loaded
strictly (unknown fields are an error), a corrupt file is never overwritten, and
`--profile NAME` is refused while it cannot be read. Order is kept.

**Why:** mcpick writes only to the user's home unless asked to edit the catalog.

**Alternatives rejected:** keeping profiles in the catalog (#14).

**Consequences:** catalog profiles are still read (#16, #17).

### 16. Catalog profiles are read-only

**Status:** superseded by #17
**Area:** files
**Decided by:** maintainer
**Date:** 2026-09-25

**Context:** after #15, profiles in the catalog came from someone else.

**Decision:** catalog profiles could be used but not changed from mcpick.

### 17. A catalog profile can be deleted from its file

**Status:** accepted
**Area:** files
**Decided by:** owner (asked for after a stray catalog profile could not be removed)
**Date:** 2026-09-26

**Context:** with #16 the only way to drop a stale catalog profile was editing
the file by hand.

**Decision:** `mcpick profile delete` and the profile screen can delete a
catalog profile from the file that holds it. Rename and reorder stay
personal-only.

**Why:** deleting is one clear edit to one entry; rename and order are
preferences.

**Alternatives rejected:** keeping them read-only (#16).

### 18. Writes go to the server's own file, with one backup each

**Status:** accepted
**Area:** files
**Decided by:** maintainer
**Date:** 2026-09-26

**Context:** servers come from several files. Deleting from the picker wrote to
the project catalog even for a server defined elsewhere, and backups named by
the second could overwrite each other.

**Decision:** edits go to the file the server came from (`Server.Source`).
Every write first makes a backup `<file>.mcpick-bak-<timestamp>` with a
nanosecond timestamp, created exclusively; the five newest are kept.

**Why:** an edit must land where the entry lives, and a backup must never be
replaced by another.

### 19. Tests cannot touch the real `~/.claude.json`

**Status:** accepted
**Area:** files
**Decided by:** maintainer
**Date:** 2026-09-26

**Context:** mcpick reads and writes `~/.claude.json`; a test with a wrong home
would edit the developer's real file.

**Decision:** the `TestMain` of catalog, cli and tui calls
`testguard.ClaudeJSON()`, which records the real file (and its backups) before
the tests and fails the run if a test changed it.

**Why:** a test bug that touches a user's Claude Code configuration must not
pass unnoticed.

### 20. Moving a server between project, local and user

**Status:** accepted
**Area:** files
**Decided by:** owner asked for the feature; maintainer designed it
**Date:** 2026-09-26

**Context:** a server often starts in the wrong place.

**Decision:** `v` in the picker (then `p`, `l` or `u`) and `mcpick move` move a
server. The destination is written first, then the entry is deleted from its
source; if the delete fails the error says the server is now in both. Moving
secrets into the project catalog asks for a typed `yes`, or `r` to write
`${VAR}` references instead (`--yes` / `--redact` on the command line), and is
refused without a terminal.

**Why:** a failed move must never lose a server, and secrets must not slip into
a shared file.

**Alternatives rejected:** delete first (a failed write loses the server).

**Consequences:** moving to user asks for no typed confirmation.

### 70. No selection is read from inside the project

**Status:** accepted
**Area:** files
**Decided by:** maintainer
**Date:** 2026-09-26

**Context:** #10 kept reading `<root>/.tmp/mcpick-<uid>.json` as the 0.1.0
location. The released 0.1.0 already wrote to `~/.mcpick/selections`; the
`.tmp` default belonged to a pre-release build. The path is inside the
repository, so a repository could ship a selection there — pre-checked
servers, and `checks` recorded for its own servers so they were confirmed
at load.

**Decision:** nothing inside the project is read as a selection. The one
legacy location left, `$XDG_STATE_HOME/mcpick/`, gives its names only: its
`checks` are dropped, since no build that wrote there recorded any.

**Why:** a selection is the user's consent; a file the repository controls
cannot carry it.

**Alternatives rejected:** keeping the `.tmp` path and ignoring `checks`
there (the names alone still pre-check servers the repository chose, and
no released version wrote the file).

### 72. An edit never strands a YAML alias

**Status:** accepted
**Area:** files
**Decided by:** maintainer
**Date:** 2026-09-26

**Context:** a YAML catalog can share a spec between servers with an
anchor and an alias (`b: &b …`, `c: *b`). Deleting `b` — `d`, a move out
of the catalog, a catalog profile's delete — removed the anchored node and
encoded the rest, writing `*b` with no `&b`: a file no YAML reader
accepts, so every server in it was gone at the next load.

**Decision:** an edit that would remove (or write over) a node holding an
anchor still used elsewhere in the file is refused, naming the anchor and
saying to edit the file by hand. A move checks this before it writes the
destination, so a refused move leaves both files as they were. Every YAML
write is also read back before it replaces the file: bytes that do not
parse, or that do not hold exactly the servers the edit meant to leave, are
not written.

**Why:** the catalog is shared through git; a picker key must not be able
to make it unreadable.

**Alternatives rejected:** expanding the aliases into copies before the
edit (rewrites parts of the file the user did not touch, and loses the
sharing the author chose).

## Security

### 21. No stdio command is trusted by default

**Status:** accepted
**Area:** security
**Decided by:** owner
**Date:** 2026-09-25

**Context:** measuring a stdio server runs its command. A catalog from a cloned
repository can name any command.

**Decision:** no stdio command runs from a measurement until trusted, including
the user's own servers and plugins. Trust is remembered in
`~/.mcpick/state/trust.json` (0600), keyed by a fingerprint (#23). Launching is
unchanged: selecting a server for a session is consent. The picker marks
untrusted commands `trust?`; `--trust` trusts from the command line; `doctor`
exits 1 when it skipped a server.

**Why:** opening a picker must never execute code the user has not seen.

**Alternatives rejected:** trusting the user's own files or plugins (a
repository can plant either).

### 22. Trust fingerprint on env keys

**Status:** superseded by #23
**Area:** security
**Decided by:** maintainer
**Date:** 2026-09-25

**Context:** the first fingerprint had to avoid storing secrets.

**Decision:** it covered the command, the arguments and the env keys only.

### 23. Trust fingerprint on the command as written

**Status:** accepted
**Area:** security
**Decided by:** maintainer
**Date:** 2026-09-25

**Context:** with keys only, changing an env value (for example a path the
command loads) kept the trust. A relative command could point at different
binaries in different directories.

**Decision:** the fingerprint is SHA-256 over command, arguments and env values
as written. A relative command with a path separator is resolved against the
working directory first; absolute commands and those starting with `$` or `{`
stay as written.

**Why:** any change to what runs asks again.

**Alternatives rejected:** hashing expanded values (would put secrets in the
hash input and change with the environment).

### 24. `m m` trusts and runs at once

**Status:** superseded by #25
**Area:** security
**Decided by:** maintainer
**Date:** 2026-09-25

**Decision:** pressing `m` twice on an untrusted server trusted it and measured
it.

### 25. `m m` opens a consent screen

**Status:** accepted
**Area:** security
**Decided by:** maintainer
**Date:** 2026-09-25

**Context:** a double key press is too easy to make by accident.

**Decision:** `m m` opens a screen with the whole command; `y` consents. Control
characters and look-alike characters are quoted so the screen shows what will
actually run. The check happens before any expansion.

**Why:** consent must follow reading, not a reflex.

### 26. Credentials masked on the consent screen

**Status:** superseded by #27
**Area:** security
**Decided by:** maintainer
**Date:** 2026-09-25

**Decision:** the first consent screen masked values that looked like
credentials.

### 27. Consent shows everything; other output masks

**Status:** superseded by #60
**Area:** security
**Decided by:** maintainer
**Date:** 2026-09-25

**Context:** a masked value can hide exactly what makes a command dangerous.

**Decision:** consent screens show the command whole and as written. The
`--trust` notice masks env values; all other output masks credentials.

**Why:** you cannot consent to what you cannot see, and a notice on stderr ends
up in logs.

### 28. Repository remotes measured only once checked

**Status:** superseded by #29
**Area:** security
**Decided by:** maintainer
**Date:** 2026-09-25

**Context:** measuring a remote server from a repository catalog sends a request
to an address the repository chose.

**Decision:** remote servers from the project catalog were measured only after
being checked.

### 29. Harmless repository remotes measured unchecked, plus `T`

**Status:** superseded by #56
**Area:** security
**Decided by:** owner (both options); maintainer (details)
**Date:** 2026-09-26

**Context:** #28 left most remotes unmeasured though requesting them exposes
nothing. Two fixes were proposed: measure the harmless ones, or let the user
trust the whole catalog. The owner chose both.

**Decision:** a repository remote is measured unchecked unless it exposes
something: a `${VAR}` in it, a private address, a proxy in the way, or an
address mcpick cannot parse. Those wait for a check. `T` in the picker and
`--trust-catalog` trust the catalog as a whole, keyed by its hash.

**Why:** measurements stay useful without letting a repository reach private
networks or read the environment.

**Alternatives rejected:** only one of the two options.

### 30. `{UUID}` is not an exposure

**Status:** superseded by #31
**Area:** security
**Decided by:** maintainer
**Date:** 2026-09-26

**Decision:** #29 first let a remote with `{UUID}` in it be measured unchecked.

### 31. `{UUID}` keeps a remote behind the check

**Status:** accepted
**Area:** security
**Decided by:** maintainer
**Date:** 2026-09-26

**Context:** `{UUID}` is replaced by `--uid`, which defaults to the hostname.

**Decision:** a repository remote containing `{UUID}` counts as an exposure and
waits for a check.

**Why:** sending the hostname to an address a repository chose is a leak.

### 32. Public-only measurements refuse private addresses

**Status:** accepted
**Area:** security
**Decided by:** maintainer
**Date:** 2026-09-26

**Context:** a public host name can resolve to a private address at connect
time.

**Decision:** a remote measured without a check is dialled with a hook that
refuses private addresses when the connection is made, not only when the URL is
read.

**Why:** closes DNS rebinding and redirects into the local network.

### 33. Borrow Claude Code's OAuth tokens, never refresh them

**Status:** accepted
**Area:** security
**Decided by:** owner (on the maintainer's proposal)
**Date:** 2026-09-24 or earlier (in 0.1.0)

**Context:** remote servers that need OAuth cannot be measured without a token,
and the user has usually signed in through Claude Code already.

**Decision:** for measuring only, mcpick reads Claude Code's token from
`~/.claude/.credentials.json` or the macOS Keychain, and only when both server
name and URL match. An expired token is reported, never refreshed.

**Why:** refreshing would rotate the token and sign Claude Code out.

**Alternatives rejected:** refreshing on the user's behalf.

### 34. No measurement survives a run

**Status:** accepted
**Area:** security
**Decided by:** owner
**Date:** 2026-09-24 or earlier (in 0.1.0)

**Context:** a cached tool list can be stale or come from another server that
used the same name.

**Decision:** the picker starts every run with no measurements.

**Why:** a number on screen always comes from this run.

**Alternatives rejected:** a measurement cache on disk.

### 35. `serve` listens on loopback and checks Origin

**Status:** accepted
**Area:** security
**Decided by:** maintainer
**Date:** 2026-09-24 or earlier (in 0.1.0)

**Context:** `mcpick serve` exposes the selected servers, with their
credentials, as one MCP server.

**Decision:** it binds to loopback unless `MCPICK_SERVE_ALLOW_REMOTE=1`, and
accepts a request only with no Origin, a `localhost` Origin or a loopback IP.

**Why:** a web page must not reach the user's MCP servers through the browser.

### 36. End-to-end tests carry no credentials

**Status:** accepted
**Area:** security
**Decided by:** owner
**Date:** 2026-09-25

**Context:** the e2e suite installs real agents in a container.

**Decision:** nothing security-related from the machine or the project enters
the container: no tokens, no keys, no login. The tests check what each agent is
handed, with fake MCP servers.

**Why:** a test image must be safe to build anywhere and to publish.

**Alternatives rejected:** mounting the user's agent logins to test real
sessions.

**Consequences:** the npm agents are installed unpinned; a gap an agent is known
to have (for example `agy`) is expected to fail.

### 37. E2E build context as a denylist

**Status:** superseded by #38
**Area:** security
**Decided by:** maintainer
**Date:** 2026-09-25

**Decision:** the first e2e image excluded known sensitive files from the build
context.

### 38. E2E build context as an allowlist

**Status:** accepted
**Area:** security
**Decided by:** maintainer
**Date:** 2026-09-25

**Context:** a denylist lets in any file nobody thought of.

**Decision:** `e2e/Dockerfile.dockerignore` excludes everything (`*`) and lets
back only Go sources and module files. `e2e` is its own Go module.

**Why:** enforces #36 by construction.

### 55. Project files are private; Gemini expands its own env references

**Status:** accepted; the existing file's mode superseded by #74, the reference forms by #75
**Area:** security
**Decided by:** maintainer
**Date:** 2026-09-26

**Context:** the project-file agents (gemini, antigravity, grok, devin) get
the selection written into a file in the repository, expanded: `${TOKEN}`
becomes the token. The file was created 0644 in a 0755 directory, readable by
every user on the machine, and Gemini's `settings.json` held values Gemini
could have expanded itself.

**Decision:** a file mcpick creates is 0600 and a directory it creates 0700.
An existing file keeps its mode; when it is group- or world-readable and a
`${VAR}` was expanded into it, the launch says so. For Gemini, an env value
made of literal text and plain `${NAME}` references is written unexpanded,
because Gemini's documentation ("Environment variable expansion" in
`docs/tools/mcp-server.md`) says it expands `$NAME` and `${NAME}` in the
`env` block, an unset variable becoming the empty string, as it does here;
`${NAME:-default}`, `${NAME:?why}`, `$${NAME}`, `{UUID}` and every other key
(headers, url, args) stay expanded, since Gemini documents no expansion
there. The other three agents document no expansion and get everything
expanded.

**Why:** the file sits in a checkout, often untracked and readable; the mode
is the one protection that needs nothing from the agent. Leaving a reference
to the agent keeps the secret out of the file altogether, but only where the
agent is documented to expand it — guessing would break a launch silently.

**Alternatives rejected:** changing the mode of an existing file (it is the
user's; a mode they chose must not change under them); writing placeholders
for every agent (grok, devin and antigravity would hand `${VAR}` to the
server verbatim).

### 56. A check is bound to origin and fingerprint; `T` hashes what was parsed

**Status:** accepted
**Area:** security
**Decided by:** maintainer (pre-release review findings A2 and A7)
**Date:** 2026-09-26

**Context:** under #29 a check was the consent to measure a repository
remote, and the saved selection held names only. A repository `.mcp.yaml`
that defined a server named like a checked user server inherited the check,
and a bare `m` sent the user's `${VAR}` to the repository's host; a checked
repository server could change its URL or headers and keep the check; `run
-y` handed the swapped server to the agent. Separately, `T` hashed the
catalog files on disk at the moment of trusting while the screen showed the
catalog as loaded earlier, so a file edited in between was trusted unseen.

**Decision:** the rules of #29 stand — harmless remotes are measured
unchecked, the rest wait for a check or `T` — with the check and the trust
bound to what they approve. The saved selection records each checked
server's origin and a SHA-256 of its spec as written; a project server
counts as checked, for measuring and for launching from the saved
selection, only while both match. A mismatch opens the row unchecked with
the reason, and drops the server on the command line with a stderr warning.
Old selections load with their project servers unconfirmed. The catalog
keeps the bytes it parsed; the catalog trust hashes those, and `T` refuses a
catalog that changed on disk since the load.

**Why:** consent is for a thing seen, and a name is not the thing.

**Alternatives rejected:** hashing expanded specs (secrets in the hash
input, and a rotated token would ask again); binding the user's own servers
too (they are the user's to edit, and no third party is reached); letting
`T` hash the disk and show a diff (the screen would still approve what was
not on it).

**Consequences:** `-y` after a pull that renames or edits a repository
server launches without it, and says so.

### 57. `--all` and catalog profiles are not consent to measure

**Status:** accepted
**Area:** security
**Decided by:** maintainer (pre-release review, section C)
**Date:** 2026-09-26

**Context:** `mcpick --all measure` and `--profile <catalog profile>
measure` selected every repository remote, and a selected remote was
measured, environment expanded into URL and headers.

**Decision:** only a check in the picker, `--select NAME`, a personal profile,
or a saved check that still covers the server (#56) is consent to measure a
repository remote that is not harmless. `--all` and a catalog profile select
without consenting: under them `measure` and `doctor` skip such a remote, and
the warning says what counts. Launching with them is unchanged: the
selection is the consent to launch.

**Why:** neither names a server, and the repository decides what either
covers.

**Alternatives rejected:** refusing `--all` for measuring altogether
(harmless remotes and trusted commands are still worth measuring).
### 58. `measurements.json` keeps the kind of an error, and a masked message

**Status:** accepted
**Area:** security
**Decided by:** maintainer
**Date:** 2026-09-26

**Context:** #34 says no measurement survives a run, yet
`~/.mcpick/state/measurements.json` exists: `measure` and `doctor` write it so
that `list` can show a cost next to each server without connecting. The
picker never reads it. The pre-release review found that an error written
there carried the expanded URL of the request — `?key=${K}` with the value
in it — and that the file outlives the run.

**Decision:** the file stays, for `list` only. Per server it holds the tool
count, the token estimate, whether the measurement succeeded, where the
credentials came from (`auth`), the time, the spec hash the entry is keyed
on, and for a failure its kind (`refused`, `401`, `timeout`, …) plus the
message with credentials masked: URL passwords, secret-sounding query
values and tokens with well-known prefixes become `***`. Nothing else is
persisted; a skipped server is never written.

**Why:** `list` is useful with a number beside each server, and #34 is about
the picker, which still measures afresh. The message is kept because `list
--json` and the kind alone cannot say which host refused; it is masked
because a state file is backed up, synced and pasted into bug reports.

**Alternatives rejected:** dropping the file (loses `list`'s costs); keeping
the kind only (loses the reason).

### 59. `import` redacts by default

**Status:** accepted
**Area:** security
**Decided by:** owner
**Date:** 2026-09-26

**Context:** `mcpick import` copied specs from `~/.claude.json` verbatim,
credentials included, into a file that usually sits in git, and needed
`--redact` to do otherwise. A move into the same file asks first (#20), and
CONTRIBUTING says the default answer to copying a credential is no; `import`
contradicted both.

**Decision:** `import` rewrites credentials as `${NAME:?export NAME}`
references and names the variables to export. `--yes` copies the values as
they are and warns, naming what it copied. `--redact` is kept as an accepted
no-op, so a script written for 0.1.0 still runs. The `:?export NAME` form is
what a redacting `move` writes too: a launch with the variable unset fails
saying which one, instead of sending an empty `Bearer `.

**Why:** the safe outcome must be the one that needs no flag. There was no
earlier decision on `import`; this is the first.

**Alternatives rejected:** keeping `--redact` as the opt-in (the review found
it was not being used); refusing `import` without a flag (a first run should
work).

### 60. Consent shows everything; every other output masks arguments and env

**Status:** accepted
**Area:** security
**Decided by:** maintainer
**Date:** 2026-09-26

**Context:** #27 had the `--trust` notice mask env values only, so a
credential written in `args` (`--api-key VALUE`) was printed on stderr,
which ends up in logs.

**Decision:** as #27, with one change: the `--trust` notice uses the same
mask as every other output nobody consents on (`MaskAll`): arguments and
env values that look like credentials are `****`. The consent screens are
unchanged and show the command whole.

**Why:** stderr is not a consent screen; the reason #27 gave for masking env
values applies to arguments just the same.

### 66. A catalog trust carries over the picker's own writes

**Status:** accepted; which rows follow the file superseded by #69
**Area:** security
**Decided by:** maintainer
**Date:** 2026-09-26

**Context:** under #56 the catalog trust (`T`) is keyed on the bytes the
catalog was parsed from, so any write lapsed it — a write the picker
itself made included. After `+`, `d`, `v` or a catalog profile's delete the
user was asked to trust again what they had just done on screen.

**Decision:** a write of the picker's own carries the trust over to the
new bytes, and the status line says so (`repo trust carried over to the
edited catalog`). The carry-over is decided before the write, on whether
the files on disk are still the bytes the picker loaded; an edit made
outside mcpick, before or after, lapses the trust as any other, and the
rows the picker cannot match to the files are not taken over.

**Why:** the trust covers what was seen; an edit made on screen was seen.
An outside edit was not, and the check is made on the bytes before the
write so that it cannot be covered by accident.

**Alternatives rejected:** keeping the lapse (a screen that asks again for
what it just did teaches the user to press `y` without reading); carrying
over on every resnapshot (an outside edit merged by the write would be
trusted unseen).

### 69. After a picker write, only the rows it wrote follow the file

**Status:** accepted
**Area:** security
**Decided by:** maintainer
**Date:** 2026-09-26

**Context:** after a write of its own (#66) the picker re-read the catalog
and copied every project server's spec from the files into its rows. An
edit made elsewhere to a checked server — its URL pointed at another host,
the user's token in a header — was adopted by the next unrelated `+`: the
row, the check recorded for it and the measurement all followed the new
spec, and the token was sent to a host the user never saw. The carry-over
itself was decided before the write, so an edit landing between that
check and the write rode along with it.

**Decision:** after a write, only the rows the write put in the file (`+`
and a move into the catalog) take their spec from the file, and even they
must be what was written. Every other project row must be in the files
exactly as loaded (same fingerprint, same file), and the files must hold
no other server. When they do not, nothing is adopted: the rows stay as the
user saw them, no trust is carried over or held for the rest of the
session, `T` refuses, and the status says `the catalog changed on disk;
reload to review it`.

**Why:** a check and a catalog trust cover what was on screen; the write
is the only change the picker saw being made.

**Alternatives rejected:** adopting the files and dropping the checks of
the rows that changed (a silent change of selection); comparing the bytes
before the write only (#66; the race stays open).

### 71. Credentials are found by their form, and around a placeholder

**Status:** accepted
**Area:** security
**Decided by:** maintainer
**Date:** 2026-09-26

**Context:** secret detection (#59) looked at a value's key first, so a
credential under a key that does not sound secret was copied into the
catalog: `AUTH_HEADER: "Bearer …"`, `X-Custom: "Bearer …"`, and a header
passed to a command as `-H 'X-Api-Key: …'`. And a value holding one
`${VAR}` was skipped whole: `?user=${USER}&token=secret` kept the token.

**Decision:** a value of the form `Bearer|Basic|Token <8+ characters>` is a
credential whatever its key, in env, headers and args; its scheme stays in
the catalog and the rest becomes the variable. After `-H` or `--header`
(and in `--header=`) the argument is read as `Name: value` and redacted by
the header's name, as a key in `headers` would be, or by the value's form.
A `${VAR}` reference hides only the match that holds it — a query value, a
URL password, a Bearer credential — and the rest of the value is searched.
`MaskText` also masks `Bearer <token>` in free text.

**Why:** the form of a value says more than an arbitrary key name; a
reference in one parameter says nothing about the next.

**Alternatives rejected:** redacting a match that is only partly a
reference (`abc${X}`): the variable would have to hold the reference's
text, which the launch does not expand.

### 73. Expanded values are put back as references in error text

**Status:** accepted
**Area:** security
**Decided by:** maintainer
**Date:** 2026-09-26

**Context:** #58 kept secrets out of error text by showing the URL as the
catalog wrote it and masking what looks like a credential. A value
expanded into a spec still came back whenever something echoed it: an
HTTP 401 body quoting the `Authorization` header it got, a resolver's
`lookup nonexistent-<token>.invalid`, a stdio server's stderr, a secret in
a URL's path. `serve` also dialled without the URL as written.

**Decision:** every probe (picker, `measure`, `doctor`) and every `serve`
upstream error has the values expansion put into the spec replaced by the
reference they came from (`${NAME}`), in the forms a URL or resolver gives
them (as is, lower-case, escaped); a header attached since (a stored or
Claude Code token) becomes `***`. Values shorter than 6 characters are
left, since they would rewrite ordinary words. `serve` dials with the URL
as written, as the picker and `measure` do.

**Why:** the one reliable way to know a secret in error text is to know
the value that was sent.

**Alternatives rejected:** more masking patterns alone (a token without a
recognisable form, or in a host name, still leaks).

### 74. An existing project file is private for the run

**Status:** accepted
**Area:** security
**Decided by:** maintainer
**Date:** 2026-09-26

**Context:** under #55 an existing project file (`.gemini/settings.json`,
`.grok/config.toml`, `.devin/mcp_config.json`) kept its mode for the run,
and the launch only said so when others could read it. For the length of
the run a `0644` file held the selection expanded — tokens included —
readable by every user on the machine.

**Decision:** an existing file that is group- or world-readable is written
`0600` for the run, with a note saying so. Its original mode is kept in the
restore record and comes back with its contents: on exit, and by the stale
recovery at the next launch (or `mcpick restore`) after a crash.

**Why:** the mode is the one protection that needs nothing from the agent;
restoring it keeps the file the user's.

**Alternatives rejected:** warning only (#55; the secret is readable in
the meantime); leaving the file `0600` after the run (changes a file that
is not mcpick's).

### 75. Gemini gets `${NAME}` for the set `:?` and `:-` forms

**Status:** accepted
**Area:** security
**Decided by:** maintainer
**Date:** 2026-09-26

**Context:** under #55 only plain `${NAME}` references in a Gemini env
value were left for Gemini to expand. `import` and a move into the catalog
write `${NAME:?export NAME}` (#59), so every imported credential was
written expanded into `.gemini/settings.json` after all.

**Decision:** `${NAME:?why}` and `${NAME:-default}` are written as
`${NAME}` when `NAME` is set and not empty: both then mean its value, as
`${NAME}` does in Gemini. With `NAME` unset or empty the `:?` form has
already failed the launch, and the `:-` form means its default, which
Gemini would not give, so the value is written expanded. A rewritten value
is kept only when expanding it gives the value the selection holds.

**Why:** the catalogs mcpick writes itself use these forms; the benefit of
#55 has to reach them.

**Alternatives rejected:** writing `${NAME:-default}` for Gemini to expand
(Gemini documents no default syntax).

## UX

### 39. Servers disabled in Claude Code

**Status:** accepted
**Area:** UX
**Decided by:** owner
**Date:** 2026-09-24 or earlier (in 0.1.0)

**Context:** Claude Code can disable a server, and a disabled server does not
load even when passed with `--mcp-config`.

**Decision:** when the agent is `claude`, the picker starts with disabled
servers unchecked and marks them. Checking one in the picker enables it in
Claude Code for good. A disabled server selected without the picker
(`--select`, `--profile`, `-y`) runs under `<name>_mcpick` for that session,
with a note that `mcp__<name>__*` permission rules do not apply to it.

**Why:** a check in the picker is an explicit request; a flag should not change
Claude's settings silently.

**Consequences:** for other agents a server disabled in Claude Code is an
ordinary one.

### 40. Unbracketed paste: the first keys act

**Status:** superseded by #41
**Area:** UX
**Decided by:** maintainer
**Date:** 2026-09-26

**Context:** in a terminal without bracketed paste, pasted text arrives as
keys, and picker keys act.

**Decision:** bursts were detected after 8 keys, so the first 8 of a paste still
acted. This was left as a known limit.

### 41. Command keys are held until a burst is ruled out

**Status:** accepted
**Area:** UX
**Decided by:** maintainer
**Date:** 2026-09-26

**Context:** with #40 a paste could still select, hide or delete.

**Decision:** bracketed paste is text. Keys that arrive in the first 250 ms
are typeahead meant for the shell or the agent, and are not acted on. A command key is held for 40 ms; if 8 keys arrive within that window
it is a burst and is treated as text.

**Why:** text must never be taken as commands.

**Consequences:** command keys act 40 ms late, too short to notice.

### 42. Inline drawing, clipped to the window

**Status:** superseded by #43
**Area:** UX
**Decided by:** maintainer
**Date:** 2026-09-26

**Context:** a line wider than the window wraps and breaks the redraw.

**Decision:** the picker drew inline and clipped every frame to the window
(`fitFrame`).

### 43. Draw on the alternate screen

**Status:** accepted
**Area:** UX
**Decided by:** maintainer
**Date:** 2026-09-26

**Context:** after leaving the profile screen, its header stayed above the
list: a frame as tall as the window scrolled the terminal, and lines that
scrolled off cannot be erased.

**Decision:** the picker draws on the alternate screen; `fitFrame` stays.

**Why:** nothing scrolls, and on exit the terminal is back where it was before
the agent starts.

### 44. Filter the list in place

**Status:** accepted
**Area:** UX
**Decided by:** owner
**Date:** 2026-09-25

**Decision:** typing a filter narrows the list as you type and shows
`N of M`.

**Why:** the result is visible before committing to it.

### 45. Unsupported and partly supported agents are said plainly

**Status:** accepted; the antigravity part superseded by #64
**Area:** UX
**Decided by:** maintainer
**Date:** 2026-09-25

**Context:** some agents cannot take a selection.

**Decision:** Devin's cloud product is not supported: its servers live
server-side and a cloud agent cannot reach the user's machine. For
`antigravity`, mcpick warns at launch that the `agy` CLI reads
`~/.gemini/config/mcp_config.json`, not the rewritten file, so it loads the
global servers. `-y` with no saved selection warns that nothing is selected.

**Why:** a silent partial result is worse than no result.

### 61. Group names follow Claude Code's scopes (project/local/user)

**Status:** accepted
**Area:** UX
**Decided by:** owner
**Date:** 2026-09-26

**Context:** 0.1.0 called the groups `workspace` (`.mcp.yaml` / `.mcp.json`
in the repository), `project` (`~/.claude.json` → `projects[<root>]`) and
`global` (`~/.claude.json` → `mcpServers`). Claude Code calls the same three
scopes `project`, `local` and `user` (`claude mcp add --scope`), so mcpick's
`project` was Claude's `local`, and a user reading both had to translate.

**Decision:** the groups are named as Claude Code names them: `project`,
`local`, `user`; `plugin` stays. The `catalog.Origin*` constants and their
values, the picker's headings, the `v` chooser (`p`, `l`, `u`), `mcpick move
<server> <project|local|user>`, `list` and the `origin` values of `list
--json` all follow, before the names become an API.

**Why:** one vocabulary across Claude Code and mcpick; the old `project`
meant two different things in the two tools.

**Alternatives rejected:** keeping 0.1.0's names with a note (the ambiguity
of `project` stays); aliases for the old `list --json` values (two names for
one thing, for a consumer that does not exist yet).

**Consequences:** `list --json` consumers reading `origin` have to follow
(listed under Changed in the changelog). `move` is unreleased and takes no
aliases. The origin a check records (#56) is unreleased too, so nothing
0.1.0 saved is affected.

### 62. Consent vocabulary: `run?`, `ask`, review commands, trust repo

**Status:** accepted
**Area:** UX
**Decided by:** owner
**Date:** 2026-09-26

**Context:** the marks and keys of #21, #25 and #29 were ambiguous before
release. A command waiting for `m m` said `trust?` and a repository remote
waiting for a check said `check`, while `T trust catalog` did not trust the
`trust?` rows, `M trust all` did not trust everything, and "check" meant
three things: the mark, the tick in the box, and a failed measurement
("check failed" in the legend).

**Decision:** one word per thing. A command waiting for `m m` is `run?`; a
repository remote waiting for a tick or `T` is `ask`, since measuring it
would send the user's environment. The key line says `M review commands`
and `T trust repo`, or `T untrust repo` while the repository's catalog is
trusted — the label follows the state. The legend reads `401 HTTP error`,
`run? runs a command: m m` and `ask sends your env: tick it or T` (short:
`run? m m`, `ask tick or T`). The `T` and `M` screens, detail lines and
statuses use the same words: a box is ticked, commands are reviewed, the
repo is trusted or untrusted. The `trusted` tag stays on the Project
heading.

**Why:** a mark should say what lifts it, and a key should say what it does
in the current state.

**Alternatives rejected:** keeping `trust?` / `check` with a longer legend
(the legend is dropped first on a narrow terminal); one mark for both
(they are lifted by different things).

### 63. `--agent` and `mcpick agents`; `--target` and `targets` kept as aliases

**Status:** accepted
**Area:** UX
**Decided by:** owner
**Date:** 2026-09-26

**Context:** the option naming the agent was `--target` in 0.1.0, gained
`--backend` as a second name with the registry (#3), and the command listing
the agents was `mcpick targets`. Everywhere else — the picker's badge, the
docs, the error messages — the thing is called an agent.

**Decision:** the documented flag is `--agent NAME` and the command is
`mcpick agents`. `--target` and `targets` stay as accepted, undocumented
aliases, since both shipped in 0.1.0 (docs/reference.md names `--target`
once, as the former name); `--backend` is removed, since it never shipped.
The name is validated on every command, whether or not it renders for the
agent, so `mcpick --agent foo list` fails with `unknown agent "foo" (mcpick
agents lists them)`.

**Why:** one word for one thing, and a typo in the agent name should fail
where it was typed, not on the next `run`.

**Alternatives rejected:** keeping `--target` (it names the agent, but a
"target" in the usage text reads as the command being launched); keeping
`--backend` as a third alias (it is an implementation word, and it never
shipped); validating only on `run` and `export` (a typo on `list` would then
be silent).

**Consequences:** the completions, the man page and the usage text list
`--agent` and `agents` only. #3's status line notes the superseded flag.

### 64. The Antigravity CLI is not supported and launched unchanged

**Status:** accepted
**Area:** UX
**Decided by:** maintainer
**Date:** 2026-09-26

**Context:** under #45 `mcpick run agy` rewrote `.agents/mcp_config.json`
for the run and warned that the `agy` CLI does not read it. The end-to-end
run confirmed the rewrite changes nothing for the CLI (`agy mcp list` says
"No MCP servers configured" whatever the selection), and recorded it as a
known gap: a file written into the repository, expanded secrets included,
for no effect.

**Decision:** the Antigravity CLI is not supported. `mcpick run agy`
launches the command unchanged, writes nothing into the project, and says
so in one line on stderr; the picker's command line and `mcpick agents`
say the same. The backend keeps its dialect, so `mcpick --agent
antigravity export` still writes the shape of the IDE's workspace file
(`serverUrl`), which the docs keep describing. The README and
docs/agents.md list it under "Not supported (CLI)". The end-to-end run
checks the launch — exit 0, the notice, nothing left behind — and records
"not supported" instead of a gap.

**Why:** a rewrite with no effect is a cost without a return; saying "not
supported" is plainer than a caveat under a mechanism that does not work.

**Alternatives rejected:** rewriting `~/.gemini/config/mcp_config.json`
(reaches every workspace, beyond one project's launch); keeping the
rewrite with the warning (#45; the file it writes holds expanded
credentials for nothing).

**Consequences:** `agy` stays a known agent name, so `--agent antigravity`
and the completions are unchanged. If a future `agy` takes a per-run
config, a backend can be written for it.

### 65. The first run starts from what the agent loads without mcpick

**Status:** accepted
**Area:** UX
**Decided by:** maintainer (pre-release review finding A12)
**Date:** 2026-09-26

**Context:** with no selection saved for a project and uid, the picker
opened with nothing checked, and a bare enter launched the agent with no
MCP servers, without a word. A first-time user who pressed enter to "see
what happens" lost every server they had.

**Decision:** on a first run the picker pre-checks the local, user and
plugin servers — what the agent would load without mcpick — minus those
disabled in Claude Code (for claude) and those hidden here, and never a
server the repository's catalog defines: Claude asks before loading those,
and a tick is the consent measuring and launching rest on (#56). The status
line says it is a first run. Whenever nothing is checked, the header says
`nothing checked — enter launches <agent> with no MCP servers`. `-y` with
no saved selection keeps warning and launching with nothing, as before.

**Why:** the first launch should change nothing the user did not ask for;
starting from the agent's own set does that, and the notice covers the
case where nothing is checked on purpose.

**Alternatives rejected:** refusing to launch with nothing checked (an
empty launch is a valid choice); pre-checking project servers too (a
cloned repository would then be consented to by pressing enter).

**Consequences:** the same rule holds for every agent, though only claude
reads `~/.claude.json` itself: for the others it is the nearest thing to
"what you had".

### 67. Flags after `run <cmd>` are reported; selection flags are exclusive

**Status:** accepted; the scan refined by #77
**Area:** UX
**Decided by:** maintainer
**Date:** 2026-09-26

**Context:** everything after `run <cmd>` belongs to the agent (#7), so
`mcpick run claude --select x` handed `--select x` to claude and picked as
usual, with no word; `--all --select x` resolved by precedence. A mistyped
command reached the generic backend and launched, or failed, after the
picker.

**Decision:** one of mcpick's own flags found after the agent's command is
still passed to the agent and reported on stderr: `--select after "claude"
goes to claude; mcpick options go before run`. `-y`, `--yes` and `--json`
are left out, since many agents take them. `--all`, `--none`, `--select`,
`--profile` and `-y` are exclusive: two of them are an error, not a
precedence. The command is looked up in PATH before the picker opens: a
missing one is `command not found`, with the closest agent name; an
existing one mcpick has no adapter for runs unchanged, with a note on
`--agent`. `--select` with an unknown name suggests the closest names.

**Why:** the rule of #7 stands, and the cost of it — a flag that silently
went elsewhere — is said. A choice between two selections is the user's,
not a table's.

**Alternatives rejected:** taking mcpick's flags from after the command
(the agent's own `--profile` or `--all` would be eaten); an error for a
late flag (a wrapper that always appends `--json` would never launch).

### 68. Shadowed definitions: one line per pair of files, silent for the same server

**Status:** accepted
**Area:** UX
**Decided by:** maintainer
**Date:** 2026-09-26

**Context:** a name defined in the catalog and again in `~/.claude.json`
was reported one line per name on every run: after `mcpick import`, which
copies the servers and leaves them in `~/.claude.json`, permanently.

**Decision:** the two definitions are compared; the same spec, or one the
redacted form of the other (`spec.Redact`), is nothing to warn about. The
rest are collapsed into one line per pair of files, naming the servers:
`2 servers in .mcp.yaml shadow ~/.claude.json (a, b); the catalog wins`.
`import` says that the copies are still in `~/.claude.json` and how to
end with one definition — `claude mcp remove <name>`, or `mcpick move
<name> project` next time, which moves instead of copying.

**Why:** a warning that is always there is not read; a warning about a
real difference is.

**Alternatives rejected:** `import` deleting from `~/.claude.json` (a
write to Claude's file the user did not ask for); one line per name
(the case it exists for — a repository taking a user server's name — is
also covered by the check being bound to the server, #56).

### 76. `--redact` and `--yes` together are an error

**Status:** accepted
**Area:** UX
**Decided by:** maintainer
**Date:** 2026-09-26

**Context:** `--redact` and `--yes` answer the same question for `import`
and `move` — what to do with credentials written in a spec — in opposite
ways. Given both, `move` redacted and `import` copied the values, each by
an unwritten precedence.

**Decision:** both at once on `import` or `move` is an error saying they
contradict each other; one of them has to go.

**Why:** as for the selection flags (#67): a choice between two answers is
the user's, not a table's, and here the wrong guess puts a token in git.

**Alternatives rejected:** letting `--redact` win (safe, but silently
ignores what `--yes` asked for).

### 77. Late-flag reports know `--`, values and the agent's own flags

**Status:** accepted
**Area:** UX
**Decided by:** maintainer
**Date:** 2026-09-26

**Context:** #67 reports one of mcpick's flags found after `run <cmd>`.
It scanned every argument, so it reported what was plainly the agent's:
`claude --agent reviewer`, `codex --profile work`, a prompt after `-p`
that reads `--all`, and anything after `--`.

**Decision:** the scan stops at `--`; it skips the value after a flag that
takes one, mcpick's or the agent's; and an agent's own flags of the same
name are left alone. Which flags an agent has is a short list in its
backend's `Meta.ValueFlags` (claude: `--agent`, `-p`, `--print`; codex:
`--profile`, `-p`), kept to the ones that collide.

**Why:** a warning that fires on correct usage teaches the user to ignore
it.

**Alternatives rejected:** a full copy of every agent's option grammar
(goes stale with every agent release).

## Distribution

### 46. Static, reproducible release builds and OS packages

**Status:** accepted
**Area:** distribution
**Decided by:** maintainer
**Date:** 2026-09-24

**Decision:** GoReleaser builds with `CGO_ENABLED=0`, `-trimpath` and file
times set to the commit time, merges the macOS builds into one universal
binary, and makes deb, rpm and apk packages with the per-repository identity
from `.git/config` as maintainer.

**Why:** one static binary per platform, identical when rebuilt from the same
commit.

### 47. Homebrew cask, not formula

**Status:** accepted
**Area:** distribution
**Decided by:** maintainer
**Date:** 2026-09-24

**Decision:** releases publish a cask to `cajbecu/homebrew-tap`. The upload is
skipped when `HOMEBREW_TAP_TOKEN` is not set. The cask removes the quarantine
attribute on install.

**Why:** GoReleaser's formula support is deprecated. The binary is not signed,
so without that step macOS would block it.

### 48. Snapshot versions from `git describe`

**Status:** accepted
**Area:** distribution
**Decided by:** maintainer
**Date:** 2026-09-24

**Decision:** snapshot builds use the `git describe` summary without the `v`.

**Why:** it says how far a build is from the last tag; `SNAPSHOT-<commit>` does
not.

### 49. Provenance attestation only on a public repository

**Status:** accepted
**Area:** distribution
**Decided by:** maintainer
**Date:** 2026-09-24

**Context:** GitHub refuses to store attestations for user-owned private
repositories, so the step failed after the release was already published.

**Decision:** the release workflow attests provenance only when the repository
is public.

**Why:** releases work while private and are attested once public.

### 50. CI checks vulnerabilities, the release and the Nix build

**Status:** accepted
**Area:** distribution
**Decided by:** maintainer
**Date:** 2026-09-24

**Decision:** CI runs `govulncheck`, a snapshot release and a build of the Nix
flake, whose `vendorHash` is pinned.

**Why:** a broken release or flake is found on the change that broke it, not at
tag time.

## Process

### 51. Commit identity set per repository

**Status:** accepted
**Area:** process
**Decided by:** owner
**Date:** 2026-09-24

**Decision:** commits are made as a per-repository identity in `.git/config`,
the same one the packages use as maintainer, not one set globally.

**Why:** a global rule would leak this identity into unrelated work on the same
machine.

### 52. History rewritten to one 0.1.0 commit

**Status:** accepted
**Area:** process
**Decided by:** owner
**Date:** 2026-09-24

**Decision:** the prototype history was squashed into one commit, "mcpick
0.1.0", tagged `v0.1.0`. A local backup of the earlier commits was kept; it
is not published.

**Why:** the public history starts at a release, without prototype noise.

### 53. The maintainer decides when the owner is away

**Status:** accepted
**Area:** process
**Decided by:** owner
**Date:** 2026-09-25

**Decision:** while the owner was away, the maintainer took the open decisions
on the owner's behalf and recorded them. Those are marked "maintainer" above.

**Why:** work did not stop, and every call stays reviewable.

### 54. Project artifacts in English

**Status:** accepted
**Area:** process
**Decided by:** owner
**Date:** 2026-09-24 (approximate)

**Decision:** code, comments, commit messages and documentation are in English.

**Why:** the project is meant to be published.
