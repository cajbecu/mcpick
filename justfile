version := `git describe --tags --always --dirty 2>/dev/null | sed "s/^v//" || echo dev`
flags := "-s -w -X main.version=" + version

default: build

build:
    CGO_ENABLED=0 go build -ldflags='{{flags}}' -o dist/mcpick .

build-all:
    #!/usr/bin/env bash
    set -euo pipefail
    for pair in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
        os="${pair%/*}"; arch="${pair#*/}"
        ext=""; [[ "$os" == windows ]] && ext=".exe"
        CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
            go build -ldflags='{{flags}}' -o "dist/mcpick_${os}_${arch}${ext}" .
    done
    ls -la dist/

test:
    go test ./...

race:
    CGO_ENABLED=1 go test -race ./...

cover:
    go test -coverprofile=dist/coverage.out ./...
    go tool cover -func=dist/coverage.out | tail -1

check: test
    test -z "$(gofmt -l .)"
    go vet ./...

# staticcheck and govulncheck, installed on first use
lint:
    go run honnef.co/go/tools/cmd/staticcheck@latest ./...
    go run golang.org/x/vuln/cmd/govulncheck@latest ./...

# Every OS mcpick ships for has to keep compiling; exec is the part that differs.
check-cross:
    #!/usr/bin/env bash
    set -euo pipefail
    for pair in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
        os="${pair%/*}"; arch="${pair#*/}"
        CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -o /dev/null . && echo "ok $pair"
    done

# End to end, in Docker: an image with every agent CLI and fake MCP servers
# (e2e/Dockerfile); the runner picks in the real picker under tmux, launches
# each agent with that selection and checks it loads exactly those servers.
# Needs Docker and network for the build; no credential leaves this machine.
# `just e2e -v` prints frames and output, `just e2e -only gemini,codex` narrows.
e2e *ARGS:
    cd e2e && go test ./...
    docker build -f e2e/Dockerfile -t mcpick-e2e .
    docker run --rm mcpick-e2e {{ARGS}}

# Refresh vendorHash in flake.nix after go.mod or go.sum changed (a
# Dependabot pull request, a `go get`): builds the flake with a placeholder
# hash, takes the real one from Nix's "got:" line and writes it back. Uses
# nix when installed, the nixos/nix image otherwise.
nix-hash:
    #!/usr/bin/env bash
    set -euo pipefail
    placeholder='sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA='
    old=$(sed -n -E 's/.*vendorHash = "([^"]*)".*/\1/p' flake.nix)
    setHash() { sed -i.bak -E "s|vendorHash = \"[^\"]*\"|vendorHash = \"$1\"|" flake.nix && rm -f flake.nix.bak; }
    setHash "$placeholder"
    build='nix --extra-experimental-features "nix-command flakes" build --no-link path:.'
    if command -v nix >/dev/null; then
        out=$(eval "$build" 2>&1 || true)
    else
        out=$(docker run --rm -v "$PWD":/src -w /src nixos/nix sh -c "$build" 2>&1 || true)
    fi
    got=$(printf '%s\n' "$out" | sed -n -E 's/.*got: *(sha256-[A-Za-z0-9+\/=]+).*/\1/p' | head -1)
    if [ -z "$got" ]; then
        setHash "$old"
        printf '%s\n' "$out" | tail -20
        echo "no hash in the output; flake.nix left as it was" >&2
        exit 1
    fi
    setHash "$got"
    if [ "$got" = "$old" ]; then echo "vendorHash unchanged: $got"; else echo "vendorHash: $old -> $got"; fi

install: build
    install -d ~/.local/bin
    install -m 0755 dist/mcpick ~/.local/bin/mcpick

# Drop the shell completions where the common shells look for them.
install-completions:
    #!/usr/bin/env bash
    set -euo pipefail
    install -d ~/.local/share/bash-completion/completions ~/.local/share/zsh/site-functions ~/.config/fish/completions
    install -m 0644 completions/mcpick.bash ~/.local/share/bash-completion/completions/mcpick
    install -m 0644 completions/_mcpick     ~/.local/share/zsh/site-functions/_mcpick
    install -m 0644 completions/mcpick.fish ~/.config/fish/completions/mcpick.fish

install-man:
    install -d ~/.local/share/man/man1
    install -m 0644 man/mcpick.1 ~/.local/share/man/man1/mcpick.1

clean:
    rm -rf dist
