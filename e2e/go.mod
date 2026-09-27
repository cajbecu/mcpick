// The end-to-end harness is a module of its own so that `go test ./...` in
// the main module stays fast and offline: everything here needs Docker.
module github.com/cajbecu/mcpick/e2e

go 1.25.0
