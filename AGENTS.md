# AGENTS.md

## Project Overview

`awg-server` is a Go HTTP API server for managing mixed AmneziaWG 2.0 and 3.1
VPN clients. It runs as a static binary on Ubuntu VPN servers, manages the
host's AmneziaWG kernel interfaces through the `awg` CLI, and persists client,
private profile, and usage data as JSON.

The server supports version-aware per-client CPS profiles through a
multi-interface pool: every unique immutable server-side `Profile` gets its
own `awgN` interface, while clients with the same effective profile share an
interface. Legacy 2.0 and 3.1 profiles never share an interface.

The StealthSurf NestJS backend uses this API to create, update, inspect, and delete AmneziaWG client configurations.

## Commands

- Build: `go build -o awg-server .`
- Build with a version: `make build VERSION=1.0.0`
- Test: `go test ./...`
- Race-test Go packages: `go test -race -count=1 ./...`
- Test release automation: `bash scripts/release-marker_test.sh && bash scripts/release-notes_test.sh && bash scripts/release-previous-tag_test.sh`
- Test installer transaction: `bash scripts/install_test.sh`
- Static analysis: `go vet ./...`
- Format changed Go files: `gofmt -w <files>`
- Build all release targets: `make build-all VERSION=1.0.0`

Before considering a code change complete, `go build -o awg-server .` and `go vet ./...` must pass. Run the relevant tests whenever testable behavior changes.

## Always-On Conventions

- Always reply to the user in Russian.
- Write code, comments, and project documentation in English.
- Keep the internal package dependency flow one-directional:

  ```text
  config <- awg <- {clients, usage} <- api <- main
  update <- main
  ```

- Never introduce circular dependencies or reverse imports across these boundaries.
- Prefer the Go standard library; the only existing external Go dependency is `golang.org/x/crypto`.
- Keep the API on the standard `net/http` ServeMux unless the user explicitly approves an architectural change.
- Never edit generated build artifacts (`awg-server` or files under `dist/`).
- Keep secrets, private keys, and bearer tokens out of source code, logs, fixtures, and documentation examples that could be mistaken for real credentials.
- API input accepts `2`, `2.0`, and `3.1`, but persisted client versions are
  canonical `2.0` or `3.1` only; missing persisted versions are legacy 2.0,
  while omitted new-create versions use the configured default (3.1 by
  default). Never conflate these boundaries.
- `HeaderProtectionKey` and its stored `header_key_id` reference are private
  3.1 state. `ProfileKey` is an opaque internal identity for both protocol
  versions and includes a header-key-derived digest for 3.1. Never serialize or
  expose any of them in ordinary API responses, logs, command arguments, error
  text, or realistic fixtures; send secret interface configuration only through
  stdin.
- Normal startup and `awg-server check-runtime` require the AWG 3.1 qualifier.
  Host migration is installer-only because a binary self-update cannot update
  and reload the package/module runtime.

## Key Files

| File | Responsibility |
| ---- | -------------- |
| `main.go` | CLI commands, startup sequence, dependency wiring, graceful shutdown |
| `internal/config/config.go` | Environment variable parsing and validation |
| `internal/config/range.go` | Strict unsigned-16 range value parsing for 3.1 settings |
| `internal/awg/keygen.go` | Curve25519 key generation and encoding |
| `internal/awg/version.go` | Canonical protocol version parsing |
| `internal/awg/params.go` | Public `AWGParams`, client/server config lines, and version-specific generation |
| `internal/awg/profile.go` | Immutable version-aware profiles, private header keys, and opaque profile identity |
| `internal/awg/device.go` | Low-level interface and peer operations through `ip` and `awg` |
| `internal/awg/pool.go` | Multi-interface lifecycle, port allocation, peer migration, NAT |
| `internal/awg/runtime.go` | AWG 3.1 package/tools/module capability qualifier |
| `internal/clients/storage.go` | Atomic JSON persistence for clients and server state |
| `internal/clients/manager.go` | Client CRUD, IP allocation, effective params, `.conf` generation |
| `internal/api/server.go` | HTTP server, routes, bearer authentication middleware |
| `internal/api/handlers.go` | Client, configuration, stats, delete, and health handlers |
| `internal/usage/collector.go` | Background rx/tx and handshake collection with JSON persistence |
| `internal/update/update.go` | Self-update from GitHub Releases |

## Change Routing

- API endpoints: update `internal/api/handlers.go`, register the route in `internal/api/server.go`, and update `docs/api.md`.
- Configuration variables: update `internal/config/config.go` and `docs/configuration.md`.
- Persisted client data: update `ClientData` in `internal/clients/storage.go` and the manager behavior that reads or writes it.
- AWG device behavior: update the helpers in `internal/awg/device.go` and, when lifecycle or grouping changes, `internal/awg/pool.go`.
- CPS parameters: update `AWGParams`, version-aware validation, and immutable
  `Profile` construction together. Keep client-only config rendering separate
  from server-side `ProfileKey` identity.
- Installation or deployment requirements: update `README.md` and `docs/installation.md`.

## Shared Agent Instructions

`AGENTS.md` is the canonical entrypoint for every coding agent. `CLAUDE.md` is
only a Claude Code adapter that imports this file. Detailed shared rules live in
`.ai/rules/`; agents do not automatically discover them, so they must be read
explicitly.

Before changing project code, every agent must read and follow:

- `.ai/rules/code-style.md`
- `.ai/rules/project-workflow.md`

Read every additional rule file that matches the task before editing:

- Package boundaries, AWG profiles, persistence, interface lifecycle, or concurrency: `.ai/rules/architecture.md`
- HTTP handlers, routes, request/response contracts, or status codes: `.ai/rules/api-patterns.md`
- Authentication, key handling, command execution, validation, network exposure, or sensitive data: `.ai/rules/security.md`
- Adding or changing Go tests: `.ai/rules/testing.md`
- Reviewing Go changes: `.ai/rules/code-review.md`
- Cutting or publishing a release, only when the user explicitly requests it: `.ai/rules/release.md`

More than one rule file can apply to the same task. When a rule conflicts with this file, `AGENTS.md` wins.

## Reference Documentation

- API contract: `docs/api.md`
- Environment variables and persistence shape: `docs/configuration.md`
- Host installation and deployment: `docs/installation.md`
- CI and automated release: `docs/ci-cd.md`
