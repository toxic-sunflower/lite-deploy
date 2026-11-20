# Contributing and maintaining


## Project documents

- [VISION.md](VISION.md): the idea, principles and limits.
- [ARCHITECTURE.md](ARCHITECTURE.md) and [SPEC-v0.md](SPEC-v0.md): how it works (the parts, security, the plan, executor actions, the CLI envelope).
- [DECISIONS.md](DECISIONS.md): the decision log, with reasons.
- [REQUIREMENTS.md](REQUIREMENTS.md): what the product must do, and the current state of each part.
- [PLAN.md](PLAN.md): the work queue. Start there.
- [DEPLOY-PATTERN.md](DEPLOY-PATTERN.md): the general deploy pattern and the project isolation checklist.
- [COMPARISON-PLAN.md](COMPARISON-PLAN.md): the plan for comparing with alternatives.
- [WSL-TEST.md](WSL-TEST.md): testing on a test machine in WSL.
- [RELEASING.md](RELEASING.md): how releases are made and signed.

## The code

`cmd/`, `internal/`, `schemas/` and `testdata/` hold the Go code. Testing a release on a real server: see [TESTING.md](TESTING.md).

Checks before every push:

```sh
go vet ./...
go test -race ./...
golangci-lint run ./...
```

Rules that tests enforce: `os/exec` and `syscall` are imported only in `internal/host`; no `unsafe`, no `cgo`; the committed `schemas/*.json` match the ones generated from the Go types (`go test ./internal/cli -update` regenerates them); the golden files in `testdata/` match the templates (`go test ./internal/render -update`).

All user-visible text, code comments and documentation are in English.


## Releases

See [RELEASING.md](RELEASING.md). A `vX.Y.Z` tag builds, signs and publishes a release, including the `liteup` script.
