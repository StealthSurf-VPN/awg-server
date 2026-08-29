# Testing Rules

Use risk-based tests to protect externally meaningful behavior and dangerous
state transitions with the smallest clear suite. Coverage percentage and test
count are diagnostics, not goals. There is no requirement to add one test for
every pure function, helper, error return, or input cross-product.

## Test Layers

Choose the lowest layer that proves the contract. Duplicate a scenario at a
higher layer only when the boundary itself is important.

| Layer | Use it for | It does not prove |
| --- | --- | --- |
| Pure unit | Parsing, validation, canonicalization, profile identity, routing math, and generated-value invariants | Manager, HTTP, process, or host integration |
| Component with fakes/temp files | Manager transactions, persistence, API status/redaction, command construction, and runtime orchestration | Real systemd, packages, kernel state, or client traffic |
| Stubbed shell harness | Installer/release ordering, deadlines, failure postconditions, cleanup, and recovery guidance | Real systemd jobs, DKMS reload, kernel sockets, reboot behavior, or networking |
| Disposable Ubuntu 22.04 host | Package/module migration, actual runtime qualification, reboot state, client handshakes, and traffic | Other architectures or fleet-wide rollout health unless separately exercised |

Critical seams may intentionally have layered coverage: API-to-persistence
version handling, secret redaction, migration rollback, runtime-probe cleanup,
and installer fail-stopped recovery. Do not copy a complete parser or
validation matrix into API, manager, and storage tests when one source-of-truth
matrix plus a representative boundary case proves the same behavior.

## What Deserves a Test

- A changed public API, persistence, configuration, release, or installer
  contract.
- A fixed regression, especially one involving migration, rollback, cleanup,
  authentication, secret handling, concurrency, or fail-closed behavior.
- A transaction with materially different pre-mutation, commit, and rollback
  outcomes.
- A pure helper whose behavior is non-trivial and is not already exercised
  clearly through its owning contract.

Usually omit or remove tests that only freeze an unexported representation,
repeat the same happy path at several layers, assert incidental helper call
counts, verify non-contractual usage prose, or exhaustively repeat syntax/error
partitions already owned by a lower layer. Keep a call-order assertion only
when ordering is itself a safety property.

## Required High-Risk Contracts

Keep distinct coverage for these areas when their implementation changes:

- API aliases and defaults versus canonical persisted protocol versions;
  missing disk versions remain legacy 2.0.
- Unsigned-range canonicalization (`off` to `0`, `N-N` to `N`) and rejection of
  3.1-only syntax for effective 2.0 clients.
- `ProfileKey` separation by protocol, server-applied fields, and private 3.1
  header key, while client-only fields and requested ports remain excluded.
- Private-key, PSK, and header-key handling through stdin/storage/config only,
  with public responses, argv, logs, and errors redacted.
- Restore preflight, usage snapshots, interface migration, persistence commit,
  rollback, and private header-key garbage collection.
- Runtime package/tool qualification, kernel-selected probe port, complete 3.1
  readback, bounded execution, collision handling, and cleanup using a fresh
  bounded context after an ambiguous create.
- Installer stop/disable ordering, bounded staged qualification, backup
  boundary, authenticated health/client-list gate, signal handling, and honest
  fail-stopped recovery after ambiguous systemd results.
- Signed release asset naming, manifest verification, downgrade refusal, and
  replacement only after verification.

## Test Design

1. Name the contract or regression before choosing cases.
2. Partition inputs by distinct behavior and select representative boundaries;
   avoid Cartesian products unless field interaction is the defect.
3. For stateful operations, assert the relevant state before mutation, after
   commit, and after each materially different rollback boundary.
4. For randomized generators and keys, assert invariants over enough attempts
   to exercise the behavior reliably; never assert an exact random value.
5. Reuse an existing `*_test.go` and its helpers when they express the same
   layer. Delete superseded cases and helpers rather than accumulating both.
6. Prefer an injected runner, temp directory, `httptest`, or fake executable to
   a test that depends on root, `/data`, host networking, or a loaded module.

## Project Conventions

- Co-locate Go tests with their source and use the same package when access to
  unexported behavior is necessary.
- Prefer table-driven subtests when the cases share setup and assertions; a
  single direct test is clearer for a unique transaction.
- Use the standard library only; do not add testify, gomega, or similar
  dependencies.
- Keep tests serial when they mutate environment variables, process-wide
  hooks, command paths, or shared files.
- Use synthetic keys, tokens, URLs, and client data. Never put operational
  secrets in fixtures or failure messages.
- Follow `.ai/rules/code-style.md`; comments should explain only non-obvious
  safety setup or why a regression boundary must remain.

## Verification

Run the narrowest relevant package first, then the gates proportional to the
change:

```bash
go test ./internal/<package>/...
go test ./...
go test -race -count=1 ./...
go vet ./...
go build -o awg-server .
```

Run `bash scripts/install_test.sh` for installer transaction changes and the
three `scripts/release-*_test.sh` harnesses for release automation changes.
Remove the generated `awg-server` binary before handoff; it is ignored build
output, not source.

Passing fake-runner Go tests or `scripts/install_test.sh` is local evidence
only. For a host-runtime or installer migration claim, report that a real
Ubuntu 22.04 qualification is still missing unless it was run separately. Even
`awg-server check-runtime` on the target proves package/tool/module and
temporary-interface behavior, not a client handshake, traffic, reboot
persistence, or fleet rollout.
