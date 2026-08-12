# Contributing to actx

## Development

```bash
go build ./...
go vet ./...
gofmt -l .        # should print nothing
go test ./...
```

All four must pass before opening a PR; CI runs the same checks.

## Fuzz testing

Ordinary `go test ./...` runs only each fuzz target's committed seed corpus, so the required test
suite stays deterministic. CI verifies its explicit matrix covers every `Fuzz*` target, then runs
each target separately for 10 seconds on pushes and
pull requests; the Monday 04:23 UTC schedule extends each target to two minutes. Local mutation
fuzzing uses the same package/target form:

```bash
go test ./internal/inspect -run '^$' -fuzz '^FuzzReadCodexRolloutStateMachine$' -fuzztime 30s
```

When Go minimizes a real failure, review the generated input and commit it under
`<package>/testdata/fuzz/<Target>/`; that regression then runs during ordinary `go test ./...`.
Do not commit exploratory cache contents that do not reproduce a defect.

Keep generated inputs bounded before allocating memory or creating files. Filesystem fuzz targets
must stay under `t.TempDir()`, reject absolute paths, separators, traversal, NULs, and volume
prefixes, and avoid live home/project paths. Required properties must not use `os.Chdir`, parallel
HOME mutation, wall-clock timing, permissions/ACL behavior, or unbounded directory trees; those are
flaky platform tests, not useful invariants.

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
