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
