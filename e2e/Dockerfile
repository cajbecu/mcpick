# The end-to-end image: mcpick, the fake MCP servers and every agent CLI that
# installs without a terminal. Built from the repository root:
#
#   docker build -f e2e/Dockerfile -t mcpick-e2e .
#   docker run --rm mcpick-e2e            # add -v for frames and output, -only gemini,codex to narrow
#
# Only Go source and module files enter the build context
# (Dockerfile.dockerignore is an allowlist); nothing is bind-mounted, no
# credential is copied, and every agent stops at its login prompt after
# reading its MCP configuration.

FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# The allowlist is what keeps local secrets out of the image; if anything
# else got through it, the build fails here rather than bake it in.
RUN stray=$(find . -type f ! -name '*.go' ! -name go.mod ! -name go.sum) \
 && { [ -z "$stray" ] || { echo "unexpected files in the build context:"; echo "$stray"; exit 1; }; }
ENV CGO_ENABLED=0 GOFLAGS=-buildvcs=false
RUN go build -ldflags='-s -w -X main.version=e2e' -o /out/mcpick . \
 && cd e2e && go build -o /out/fakemcp ./fakemcp && go build -o /out/e2e-runner ./runner

FROM node:22-bookworm
ENV HOME=/root TERM=xterm-256color PATH=/root/.local/bin:/root/.grok/bin:$PATH
RUN apt-get update && apt-get install -y --no-install-recommends tmux git ca-certificates curl \
 && rm -rf /var/lib/apt/lists/*

# The npm agents, at whatever version is current: the point of this image is
# to notice when one of them changes how it reads its configuration. pi's MCP
# support is an extension, installed the way pi installs extensions.
RUN npm install -g --silent @anthropic-ai/claude-code @openai/codex @google/gemini-cli \
      @github/copilot opencode-ai @earendil-works/pi-coding-agent \
 && pi install npm:pi-mcp-adapter \
 && npm cache clean --force

# The ones with an installer script; each puts a binary in ~/.local/bin (grok:
# ~/.grok/bin, symlinked). devin's script starts devin when it is done and
# that fails without a terminal, so its exit status is ignored; muse installs
# a launcher that fetches the real binary on first use, which --version does
# here rather than during the run. The version checks are the actual gate.
RUN curl -fsSL https://x.ai/cli/install.sh | bash \
 && (curl -fsSL https://cli.devin.ai/install.sh | bash || true) \
 && curl -fsSL https://dev.meta.ai/install.sh | bash \
 && curl -fsSL https://antigravity.google/cli/install.sh | bash \
 && grok --version && devin --version && muse --version && agy --version

COPY --from=build /out/mcpick /out/fakemcp /out/e2e-runner /usr/local/bin/
ENTRYPOINT ["e2e-runner"]
