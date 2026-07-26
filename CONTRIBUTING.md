# Contributing to actx

## Development

```bash
go build ./...
go vet ./...
gofmt -l .        # should print nothing
go test ./...
```

All four must pass before opening a PR; CI runs the same checks.

## Adding or updating a tool

Each supported agent (Claude Code, Codex CLI, etc.) is modeled as its own spec in
[`internal/tools/tools.go`](internal/tools/tools.go) — scope, walk direction, precedence, global
config path. If a tool's real discovery/merge behavior changes (or you're adding a new tool),
update:

1. [`docs/research.md`](docs/research.md) — the sourced findings behind the behavior, with links.
2. `internal/tools/tools.go` — the spec itself.
3. Tests in the relevant `internal/*` package and `cmd/actx`.

Claims about a tool's behavior should be traceable to that tool's own documentation or source, not
assumption — flag genuine gaps as open questions rather than guessing.

## Reporting issues

Open a GitHub issue. For a bug, include the OS, Go version, the exact command run, and
expected vs. actual output (`--json` output is easiest to compare exactly).

## License

By contributing, you agree your contributions are licensed under the project's [MIT
License](LICENSE).
