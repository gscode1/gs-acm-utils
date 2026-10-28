# gs-acm-utils

Go CLI `acm-mock-resolver`: resolves/validates OCM/ACM policy templates against mock data
(no cluster). Uses `github.com/stolostron/go-template-utils/v7` (pinned v7.3.0) as a library;
never modify upstream.

## Commands
- `make build`  -> `bin/acm-mock-resolver` (version injected via ldflags)
- `make test`   -> `go test -race ./...`
- `make lint`   -> `golangci-lint run ./...` (v2 config in `.golangci.yml`)
- `make fmt`    -> gofumpt + gci via `golangci-lint fmt`
- `make bench`  -> benchmarks

## Layout and layering rules
Everything lives under `internal/` (no public API in MVP).
- `internal/mockdata` must NOT import `pkg/templates`.
- `internal/fakecluster` depends only on `mockdata`.
- `internal/resolve` depends on both `mockdata` and `fakecluster`.
- `internal/cli` wires commands; `cmd/acm-mock-resolver/main.go` only maps errors to exit codes.

## Conventions
- Parse mock YAML with `sigs.k8s.io/yaml`, not `gopkg.in/yaml.v3`.
- Never reuse/mutate a `templates.ResolveOptions` across calls (ResolveTemplate appends to it); build a fresh one per call.
- Template strings in YAML fixtures are single-quoted: `'{{ ... }}'`.
- Commit messages end with the Co-Authored-By trailer; PR bodies end with the Claude Code line.
