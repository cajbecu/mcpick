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

This is the contribution the project most wants, and it is a small one. A
target answers five questions:

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

Then add an entry to `All()` in `internal/target/target.go`, using the type that
matches question 3:

| Question 3 answer | Type | Examples |
|---|---|---|
| A flag | `claudeTarget`-style, or a `projectTarget` with `extraArgs` | claude, gemini |
| A config-directory variable | `homeTarget` (`internal/target/overlay.go`) | codex, copilot, pi, muse, opencode |
| Nothing | `projectTarget` (`internal/target/project.go`) | antigravity, grok, devin |

`homeTarget` is preferred wherever it works: it builds a shadow of the agent's
config directory where every entry is a symlink back to the real one except the
generated config, and carries back whatever the agent writes, so the agent keeps
its credentials and sessions and mcpick never edits a file in place.

If the dialect is new, add it in `internal/spec/dialect.go` — usually a
`jsonDialect` value, otherwise an `Emit*` function. Convert through `spec.View`
rather than reaching into the raw map; that is what lets one catalog entry
render correctly for ten agents. Keys only one agent understands go in
`dialectKeys`, so they reach that agent and nobody else.

Put the documentation the adapter was written against in `Docs`. Agents change
their config formats; the link is how the next person checks.

Every target needs a test in `internal/target` proving that the generated config
carries the selection and that the agent's own settings survive.

## Changing dependencies

`flake.nix` pins a hash of the Go module dependencies (`vendorHash`). After any
change to `go.mod` or `go.sum`, set it to `pkgs.lib.fakeHash`, run `nix build`,
and paste the hash from the `got:` line. Without Nix installed:

```sh
docker run --rm -v "$PWD":/src -w /src nixos/nix \
  nix --extra-experimental-features "nix-command flakes" build path:/src
```

CI builds the flake, so a stale hash fails the pull request.

## What a good change looks like

- Tests describe the failure they prevent, not the function they call. The
  comment above a test should say why the bug would be bad.
- A test that checks what goes over the wire checks it against something that
  validates the wire format. A fake that ignores its input once let mcpick send
  `"ProtocolVersion"` instead of `"protocolVersion"` with every test green.
- No new dependencies without a reason in the pull request. mcpick deliberately
  has four, and the MCP client is hand-written so there is no SDK to track.
- `gofmt`, `go vet`, `golangci-lint` and `go test -race` are clean. CI runs them
  on Linux, macOS and Windows.
- Anything that writes outside `~/.mcpick` (see `fsutil.MCPickHome`) or
  the files a target documents needs a reason in the review.

## Secrets

Rendered configs contain expanded credentials. They are written `0600` inside a
`0700` directory and removed when the session that owns them ends. If you add a
code path that writes a config anywhere else, say so in the pull request — that
is the one part of this program where a mistake leaks a token.

Never add a feature that copies live credentials into the catalog file. `import`
only does it when asked, and `--redact` exists so the default answer is no.
