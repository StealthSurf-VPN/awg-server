# AGENTS.md

## Project Overview

`awg-server` is a Go HTTP API server for managing AmneziaWG 2.0 VPN clients. It runs as a static binary on VPN servers, manages the host's AmneziaWG kernel interfaces through the `awg` CLI, and persists client and usage data as JSON.

The server supports per-client CPS obfuscation profiles through a multi-interface pool: every unique server-side profile gets its own `awgN` interface, while clients with the same profile share an interface.

The StealthSurf NestJS backend uses this API to create, update, inspect, and delete AmneziaWG client configurations.

## Commands

- Build: `go build -o awg-server .`
- Build with a version: `make build VERSION=1.0.0`
- Test: `go test ./...`
- Race-test the complete API/package suite: `go test -race -count=1 ./...`
- Test release automation: `bash scripts/release-marker_test.sh && bash scripts/release-notes_test.sh && bash scripts/release-previous-tag_test.sh`
- Static analysis: `go vet ./...`
- Format changed Go files: `gofmt -w <files>`
- Build all release targets: `make build-all VERSION=1.0.0`

Before considering a code change complete, `go build -o awg-server .` and `go vet ./...` must pass. Run the relevant tests whenever testable behavior changes.

## Always-On Conventions

- Always reply to the user in Russian.
- Write code, comments, and project documentation in English. Keep the supplied `.ai/README.md` migration guide in Russian unless the user asks to translate it.
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

## Key Files

| File | Responsibility |
| ---- | -------------- |
| `main.go` | CLI commands, startup sequence, dependency wiring, graceful shutdown |
| `internal/config/config.go` | Environment variable parsing and validation |
| `internal/awg/keygen.go` | Curve25519 key generation and encoding |
| `internal/awg/params.go` | `AWGParams`, grouping keys, CLI args, config lines, generated CPS params |
| `internal/awg/device.go` | Low-level interface and peer operations through `ip` and `awg` |
| `internal/awg/pool.go` | Multi-interface lifecycle, port allocation, peer migration, NAT |
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
- CPS parameters: update `AWGParams` and keep `Key()`, `CLIArgs()`, and `ConfigLines()` semantics aligned.
- Installation or deployment requirements: update `README.md` and `docs/installation.md`.

## Shared Agent Instructions

`AGENTS.md` is the canonical entrypoint for every coding agent. `CLAUDE.md` is only a Claude Code adapter that imports this file. Detailed shared rules live in `.ai/rules/`; agents do not automatically discover them, so they must be read explicitly.

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
- Shared-instruction layout and migration guide: `.ai/README.md`
