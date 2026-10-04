# Adversarial review and remediation

## First-round scope and identity

Reviewed baseline: `8a979d1e492fd374ee420f07258063ee12d0fb1a`.
Baseline manifest: `sha256:64e8e8cfe22747adabf78ba8552725a60850a8dc3a728308f7142dea64163669`.
Fixed code/test snapshot: `sha256:9d97fcd24a7334a93c19ee71bb3eee75afe6931312ee0f3583c719f838bb065a`.
The latter includes the documentation as it stood before the accompanying documentation updates;
all reviewed Go files remained unchanged through final review and integration.

Model: gpt-6-astra, openai-codex. Execution: Go 1.27.1, macOS arm64.
Three prompt-blinded Phase A reviewers examined discovery/compilation, live inspection, and
CLI/render/configuration. Separate Phase B invocations validated the frozen candidate artifacts
and implemented regression-driven fixes. A fresh final reviewer challenged the combined patch;
it received remediation topics and known limitations, so that pass was not rationale-blind.

Threat model: a local CLI running with the caller's filesystem permissions, consuming untrusted
repository filenames, instruction contents, symlinks, and session artifacts. Scope included
correctness, provenance, parsing, filesystem boundaries, resource use, state transitions,
terminal output, and CI configuration. Probes used bounded synthetic fixtures, not real secrets,
production services, or upstream agent sessions. These are baseline defects, not claims that the
latest commit introduced them. No dependency or CI configuration change was needed.

## Confirmed findings and dispositions

Fourteen initial candidates reduced to thirteen distinct findings after merging the duplicate
filtered-invocation FIFO report. Each disposition has regression or paired-probe evidence.
Baseline citations below refer to the immutable revision above; current references identify the
first-round remediation snapshot. Those line numbers predate the continuation below.
P1 issues had high local confidentiality/availability impact under their stated
preconditions; P2 issues affected correctness, provenance, output integrity, or resource use.

| ID | Priority | Preconditions and baseline failure | Disposition / current implementation |
|---|---|---|---|
| SCAN-COMPILE-A01 | P1 | A matching instruction file is a writerless FIFO, directly or through a symlink; `scan.go:325-334,413-415` blocks. | Reject known non-regular entries before opening; ordinary file symlinks still work. `internal/scan/scan.go:334` |
| SCAN-COMPILE-A02 | P2 | Target/home/discovered directory names contain glob syntax; `scan.go:189-205,346-367` omits intended files or reads siblings. | Separate literal base paths from registry patterns. `internal/scan/scan.go:358` |
| SCAN-COMPILE-A03 | P2 | Both Codex base and override files exist in a project directory; `compile.go:129-153` includes both and inflates the byte budget. | Resolve same-directory overrides before assembly and limits; preserve the raw inventory. `internal/compile/compile.go:163` |
| SCAN-COMPILE-A04 | P2 | Cline global Rules directory has files; `tools.go:220-222` never enumerates them. | Enumerate direct regular rule files before workspace rules. `internal/tools/tools.go:222` |
| SCAN-COMPILE-A05 | P2 | A descendant filename sorts before its parent's rule; `scan.go:307` reverses specificity order. | Preserve deterministic parent-before-child traversal. `internal/scan/scan.go:245` |
| INSPECT-A1 | P1 | Aider history is a symlink outside the inspected project; `inspect.go:401-418` emits unrelated bytes as confirmed content. | Confine reads with `os.Root`; preserve relative in-project links, refuse escaping/absolute links. `internal/inspect/inspect.go:432` |
| INSPECT-A2 | P2 | A later Codex turn changes cwd and omits, nulls, or empties instructions; `inspect.go:348-363` retains another project's payload. | Treat turns as snapshots, not last-nonempty field merges. `internal/inspect/inspect.go:346` |
| INSPECT-A3 | P2 | A rollout has matching cwd but no instruction content; `inspect.go:292-312` claims content was recovered. | Return metadata-only when supported content fields are empty. `internal/inspect/inspect.go:309` |
| INSPECT-A4 | P2 | Aider history is large, including when excluded by `--tool`; `inspect.go:404-417` reads it fully before filtering. | Limit actual history reads to 16 MiB plus one detection byte; oversized input is metadata-only. Select tools before I/O. `internal/inspect/inspect.go:87,430` |
| INSPECT-A5 | P2 | Distinct project paths share Claude's slash-to-hyphen encoding; `inspect.go:124-179` claims their transcripts belong to the requested project. | Label transcripts/counts as candidates with unverified project scope. This does not disambiguate them. `internal/inspect/inspect.go:184` |
| CLI-JSON-ERROR | P2 | JSON mode encounters an unknown flag or invalid typed value; `main.go:236-274` prints plain usage before JSON stderr. | Defer usage until parsing finishes; error stderr is one JSON object, while help remains readable. `cmd/actx/main.go:273` |
| RENDER-TERMINAL-CONTROLS | P2 | Instructions, artifact content, or paths contain terminal controls; text rendering emits them raw. | Escape controls at text presentation, including CLI errors and overflow notices; preserve JSON values. `internal/render/terminal.go:13` |
| CLI-OVERFLOW-WRITER | P2 | Output writer fails while receiving a text overflow notice; `main.go:88-91` returns success. | Propagate writer errors, including version/tool-list sibling paths. `cmd/actx/main.go:55` |

Additional hardening: empty Aider histories are metadata-only rather than content-confirmed.
The duplicate CLI-FILTER-FIFO finding was addressed through regular-file checks and tool
selection before scan/inspection dispatch (`cmd/actx/main.go:348,360`).

## Verification

Regression cycles observed the expected failures before implementation and passed after fixes.
The parent independently reran these gates in the integrated primary checkout:

- `go test -race -count=1 ./...` — all six packages passed.
- `go vet ./...` and `go build ./...` — passed.
- `python3 scripts/check-fuzz-matrix.py` — all eleven targets covered.
- `gofmt -l cmd internal` — no output.
- `git diff --check` — passed.

All eleven mutation-fuzz targets passed ten-second, single-worker runs on the identical reviewed
code in the isolated worktree. This is fuzzing, not a mutation-testing score. The configured
mutation-testing plugin was unavailable; no such plugin result is claimed.

The final reviewer also ran nine independent target/baseline CLI probe pairs: every target
invariant passed, eight baseline cases failed the same invariants, and one pair confirmed
compatibility. It rejected eleven regression candidates and retained no actionable findings
within the remediation scope. Source hashes were checked before integration and destination
bytes matched the reviewed artifacts. No commit, push, or remote synchronization was performed.

## Continuation: resource and filesystem hardening

The follow-up addresses `actx-cag` and `actx-aw6`. It started from the first-round uncommitted
source, not bare HEAD. Baseline member manifest:
`sha256:7f162d14093954f91575ed001da26b0851598f04833bcf2f3ce71dff336d936d`.
Reviewed fixed member manifest:
`sha256:20c826cc1702a6afaf0d156f2e586ab393e2dbbb81bff5dd2c7aee166161836b`.
Documentation was updated after that manifest; all reviewed Go members retained their hashes.

Three separate implementation lanes used bounded regression-driven fixes. Two fresh read-only
reviewers then challenged input/filesystem handling and output handling. They received scope
and limitations, and the output reviewer knew about the publication fix below; this was an
independent final challenge, not a blind replication of the initial discovery.

### Changes and observed defects

- **Unbounded input/discovery:** `internal/budget` now shares finite file, byte, directory-entry
  and candidate limits across selected tools. Defaults are 16 MiB per content read, 64 MiB total,
  1,024 reads, 2,048 directory-listing attempts, 32,768 entries, and 4,096 candidate matches.
  Tests inject smaller limits and exercise exact boundaries without destructive OOM probes.
  Directory acquisition itself is batched and bounded. Scans return an error, not partial context,
  on exhaustion, including existing depth/visit ceilings. Live reports use `incomplete: true`
  without partial content or misleading latest/count claims. An unreadable or over-budget Codex
  search cannot silently promote an older rollout.
- **Stat/open FIFO replacement:** `internal/fileio` uses Unix nonblocking acquisition followed
  by descriptor validation before content reads. Scan, Codex, and project-confined Aider use the
  helper. Deterministic child-process controls replace a regular file with a writerless FIFO;
  raw opens block until killed/reaped, protected opens reject it. Regular symlinks remain usable
  and Aider links stay confined. This is not a guarantee against arbitrary device/kernel latency.
- **Whole-output buffering:** `cmd/actx/output.go` stages at most 256 KiB in memory before private
  file spill, with a separate 64 MiB staged-byte ceiling. Atomic publication can temporarily use
  two disk copies. Unicode/malformed-byte counts work across split writes. Explicit unlimited
  output streams directly; input budgets remain enabled. Tests cover prompt sink failure,
  short writes, cleanup, existing mode preservation, and no destination access below the cap.
- **Introduced publication-order defect, fixed before integration:** the first staging version
  emitted an overflow notice before replacing `--out`. A synchronous observing writer read the
  old file; a failed rename also emitted a misleading success notice. Both new regression tests
  failed before the fix. Publication now precedes the notice. A failed notice leaves the complete
  caller-chosen output published and returns an error; it cannot roll back a consumer's read.

### Continuation verification

- Parent full `go test -race -count=1 -timeout=180s ./...`, `go vet ./...`, `go build ./...`,
  formatting and diff checks passed on macOS arm64 with Go 1.27.1.
- The complete suite also passed on the module-minimum Go 1.26.5. The first offline toolchain
  resolver attempt failed because checksum verification was disabled; invoking the already
  cached toolchain binary directly resolved that infrastructure issue.
- All eleven existing fuzz targets passed ten-second single-worker runs; CI matrix coverage passed.
- All seven internal packages passed runtime tests on Linux arm64 in a network-disabled,
  read-only Alpine container with bounded temporary storage. The Windows amd64 CLI cross-build
  passed; no native Windows execution is claimed.
- Six parent CLI smoke probes verified ordinary compile output, untouched below-cap destinations,
  overflow payload/count/mode, no notice on failed publication, JSON scan-budget errors, and
  incomplete live reports that do not confirm an older rollout.
- Independent input review verified 49 distinct top-level tests, including four new overlay
  probes. Independent output review verified 41 byte-identical ordinary baseline/target command
  cases, six overflow cases, and two spill cases. Deliberately broken counting, publication and
  memory-threshold controls failed their assertions. Both final reviews retained no findings.

The input reviewer preserved an initial failing deadline probe rather than hiding it: the parent
had incorrectly described a 30-second policy, then corrected the scope against the frozen source.
No deadline exists; the implemented contract bounds acquired bytes and work counts. The corrected
independent probe set passed. Reports retain this correction and the initial failed observation.

Continuation evidence is under the session scratch directory `actx-hardening-evidence/`:
`baseline-manifest.json`, `fixed-manifest.json`, `phase-b-input-budgets.json`,
`phase-b-output-budget.json`, `fileio/phase-b-fileio.json`, `parent-publication-fix.json`,
`parent-gates.jsonl`, and `final-review-{inputs,output}.json`. Scratch evidence may be pruned;
regression tests and this report are the durable repository artifacts.

## Remaining limits

- Windows has post-open regular-descriptor validation but no Unix nonblocking-open guarantee.
  Native Windows runtime, rename, reserved-device and reparse-point behavior is not certified.
- Finite work budgets are not wall-clock deadlines for filesystem calls or arbitrary devices.
  Reads are not atomic snapshots of concurrently changing regular files.
- The staging-memory limit is not a process-wide RSS cap: compilers, encoders and formatters still
  allocate input-derived objects. Unlimited streaming can leave partial stdout on a late error.
- Abrupt termination or failed filesystem cleanup can orphan private temporary files. Atomic
  visibility does not establish power-loss durability; the destination directory is not fsynced.
- Claude project scope remains uncertain when encoded names collide. Upstream artifacts are not
  authenticated, and semi-documented live formats can change.
- Terminal escaping does not defeat every Unicode visual spoof or misleading prose. Consumers
  displaying decoded JSON must escape terminal controls themselves.

Verdict: ready within the reviewed remediation scope, subject to the explicit platform and
filesystem limitations above. No commit, push, or remote synchronization was performed.
