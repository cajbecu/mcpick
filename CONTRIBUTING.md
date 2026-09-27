# Contributing

## Getting set up

```sh
git clone https://github.com/cajbecu/mcpick && cd mcpick
just check          # gofmt, go vet, go test
just race           # the test suite under the race detector (needs cgo)
just build          # dist/mcpick
```

`just` is optional; every recipe is one `go` command. The code lives under
`internal/`; [docs/reference.md](docs/reference.md#code-layout) says which package does what.

## Adding support for another agent

This is the contribution the project most wants, and it is a small one: one
new file in `internal/backend`. A backend answers five questions:

1. **Where does the agent read MCP servers from?** Global path and project path.
2. **What dialect?** JSON or TOML, which top-level key, which key holds the
   remote URL (`url`, `httpUrl` and `serverUrl` are all in use), what the
   header table is called, and whether it needs a `type`.
3. **Can a single run be pointed at a different config?** A flag is best, an
   environment variable for the config directory is next best, nothing at all
   means the file has to be rewritten and restored.
4. **What else does it read behind your back?** Files the agent merges in on
   its own go in `AlsoReads`, so mcpick can warn that the selection is not
   exclusive.
5. **What is its command name?** That is how mcpick auto-detects it.

Then write `internal/backend/<agent>.go` with an `init` function that calls
`Register`, using the mechanism that matches question 3:

| Question 3 answer | Mechanism | Examples |
|---|---|---|
| A flag | its own type, like `claude.go`, or `projectBackend` with `extraArgs` | claude, gemini |
| A config-directory variable | `overlayBackend` (`overlay.go`) | codex, copilot, pi, muse, opencode |
| Nothing | `projectBackend` (`projectfile.go`) | grok, devin |

`devin.go` is the shortest complete example:

```go
func init() {
	Register(projectBackend{
		Meta: Meta{
			Name: "devin", Glyph: "◈", Color: "#6366F1", Docs: "https://cli.devin.ai/docs/extensibility/mcp/configuration",
		},
		file: ".devin/mcp_config.json", topKey: "mcpServers",
		dialect: spec.JSON{Agent: "devin", TopKey: "mcpServers", Type: true},
	})
}
```

`overlayBackend` is preferred wherever it works: it builds a shadow of the
agent's config directory where every entry is a symlink back to the real one
except the generated config, and carries back whatever the agent writes, so the
agent keeps its credentials and sessions and mcpick never edits a file in place.

The dialect is usually a `spec.JSON` or `spec.TOML` value; a format of its own
implements `spec.Dialect` in the backend's file (see `opencode.go`). Convert
through `spec.View` rather than reaching into the raw map; that is what lets
one catalog entry render correctly for ten agents. Keys only this agent
understands go in `Meta.Owns`, so they reach it and nobody else. A step the
agent needs before launch, beyond rendering a config, is a `Prepare` method
(see `claude.go`).

Put the documentation the adapter was written against in `Docs`. Agents change
their config formats; the link is how the next person checks.

`TestConformance` runs every registered backend through the same checks. Add a
test of your own for what is particular to the agent: its dialect in
`dialects_test.go`, anything else in `<agent>_test.go`.

## End-to-end tests

`just e2e` builds a Docker image (`e2e/Dockerfile`, tagged `mcpick-e2e`) with
mcpick, every agent CLI that installs without a terminal, and fake MCP servers
(`e2e/fakemcp`: one tool returning a nonce, a log of every `initialize`), then
runs `e2e/runner` inside it. The runner writes a catalog of five fakes, picks
three of them in the real picker under tmux, and launches each agent with
that selection through a command that reads its MCP configuration without a
model — `codex mcp list`, `gemini mcp list`, a headless prompt that stops at
the login screen — checking, from the agent's listing or from the fakes'
logs, that exactly the chosen servers were loaded. No credential is copied
or mounted into the container, and the build context is an allowlist
(`e2e/Dockerfile.dockerignore`): only Go source and module files enter the
image, never a local catalog, `.env`, agent settings or key.
`e2e/context_test.go` checks that with secret-shaped decoys, and the build
stage fails if anything else got through.

It needs Docker and the network for the build, takes a few minutes, and is
not part of `go test ./...`: `e2e/` is a module of its own. `just e2e -v`
prints the picker frames and each agent's output, `just e2e -only codex,muse`
narrows the run. The result is a table, agent × level × pass/FAIL/gap, and a
non-zero exit on any FAIL; an unknown name in `-only` is an error, not an
empty run. A `gap` is a known limitation recorded in the runner (`agents` in
`e2e/runner/main.go`, with what the probe prints when it hits it); it excuses
that failure only — a probe that times out, exits non-zero, leaves its
directory behind or fails another way is a FAIL — and when a gap starts
passing, the run fails until the note is removed.

Adding an agent to the run is one entry in `agents`: its name, the arguments
that make it read its MCP configuration without calling a model, whether the
proof is its listing or the fakes' logs, and — for the project-file
mechanism — the directory mcpick creates that must be gone afterwards. Its
install goes in `e2e/Dockerfile`.

## Changing dependencies

`flake.nix` pins a hash of the Go module dependencies (`vendorHash`). After any
change to `go.mod` or `go.sum`, set it to `pkgs.lib.fakeHash`, run `nix build`,
and paste the hash from the `got:` line. Without Nix installed:

```sh
docker run --rm -v "$PWD":/src -w /src nixos/nix \
  nix --extra-experimental-features "nix-command flakes" build path:/src
```

CI builds the flake, so a stale hash fails the pull request.

## Releasing

The version is written in one place, `VERSION` (no `v`), and everything else
takes it from there: the Nix flake reads the file, release binaries carry the
tag, and the release workflow refuses a tag that is not `v$(cat VERSION)`, so
the two cannot drift. To release:

1. Bump `VERSION`; in `CHANGELOG.md` turn `[Unreleased]` into a dated section
   and add an empty `[Unreleased]` above it, with the compare links at the
   bottom; commit.
2. Tag that commit `v$(cat VERSION)` and push the tag. The release workflow
   runs the tests and `govulncheck`, then GoReleaser builds the archives, the
   Linux packages and the Homebrew cask, publishes the GitHub release with the
   changelog, and attests the provenance of the assets.

A snapshot of the release build runs on every pull request
(`goreleaser release --snapshot`), so a broken release configuration fails
before a tag does.

## Recording decisions

[docs/decisions.md](docs/decisions.md) keeps every decision that shapes how
mcpick behaves — architecture, security rules, file locations, UX — with the
reason and the alternatives that were rejected. Read the relevant entries
before changing behaviour they cover. A change that makes or reverses such a
decision appends a new entry in the same pull request; an entry it replaces
is marked "Superseded by #N", never rewritten or removed.

## What a good change looks like

- Tests describe the failure they prevent, not the function they call. The
  comment above a test should say why the bug would be bad.
- A test that checks what goes over the wire checks it against something that
  validates the wire format. A fake that ignores its input once let mcpick send
  `"ProtocolVersion"` instead of `"protocolVersion"` with every test green.
- No new dependencies without a reason in the pull request. mcpick deliberately
  has few — Bubble Tea, Lip Gloss, a couple of `charmbracelet/x` helpers,
  `yaml.v3` and `x/sys` — and the MCP client is hand-written so there is no
  SDK to track.
- `gofmt`, `go vet`, `golangci-lint` and `go test -race` are clean. CI runs them
  on Linux, macOS and Windows.
- Anything that writes outside `~/.mcpick` (see `fsutil.MCPickHome`) or
  the files a backend documents needs a reason in the review.

## Secrets

Rendered configs contain expanded credentials. They are written `0600` inside a
`0700` directory and removed when the session that owns them ends. If you add a
code path that writes a config anywhere else, say so in the pull request — that
is the one part of this program where a mistake leaks a token.

Never add a feature that copies live credentials into the catalog file. `import`
and `move` redact by default (`spec.Redact`), and copy a value only when asked
with `--yes`; a new place that writes a spec into the catalog uses the same
function, so the default answer stays no.
