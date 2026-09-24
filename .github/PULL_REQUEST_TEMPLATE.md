## What this changes

<!-- One or two sentences. -->

## Why

<!-- The problem it fixes, or the thing it makes possible. -->

## Checklist

- [ ] `just check` passes (gofmt, go vet, go test)
- [ ] New behaviour has a test that would fail without the change
- [ ] No new dependency, or the pull request explains why one is needed
- [ ] If it writes a file anywhere: the path, mode and cleanup are described above

## If this adds a target

- [ ] The generated config carries the selection
- [ ] The agent's own settings survive the run
- [ ] `mcpick targets` describes the mechanism honestly
