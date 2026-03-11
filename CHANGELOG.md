# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.0.2] - 2026-03-11

- Added atomic peer migration (`MigratePeer`) — safely change CPS profile while keeping the same port when client is the last peer on an interface
- Renamed `name` to `id` in POST request body; removed `name` from API responses and storage
- Fixed client CPS profile update failing with "port already in use" when requesting the same port on a different profile
- Fixed port-only change on shared interface silently deleting peer; now returns 409 Conflict (`ErrPortShared`)

## [1.0.1] - 2026-03-10

- Added per-client obfuscation profiles — each unique CPS parameter set gets its own AWG interface
- Added `GET /health` endpoint for monitoring (no auth required)
- Added CLI self-update from GitHub Releases (`awg-server update`)
- Added input validation hardening: request body size limits, client ID length check
- Added port uniqueness validation (409 Conflict) and interface limit enforcement (503)
- Cleaned up dead code and unused files

## [1.0.0] - 2026-03-01

- Initial release
- HTTP API for AmneziaWG 2.0 client management (create, list, get config, delete)
- Multi-interface pool with automatic lifecycle management
- AmneziaWG CPS parameters support (Jc, Jmin, Jmax, S1-S4, H1-H4, I1-I5)
- Curve25519 key generation
- JSON file persistence with atomic writes
- Sequential IP allocation with reuse
- Bearer token authentication
- MASQUERADE NAT rule management
- Cross-platform build support (Linux, macOS, Windows; amd64, arm64)
