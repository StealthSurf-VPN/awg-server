# CLAUDE.md - AmneziaWG Server

## Project Overview

**awg-server** is a Go HTTP API server for managing **AmneziaWG 2.0** VPN clients. It uses the **AmneziaWG kernel module** on the host and the `awg` CLI tool for device and peer management, providing near-native WireGuard performance with DPI obfuscation via CPS (Custom Protocol Signature).

Supports **per-client obfuscation profiles** — each unique set of CPS parameters gets its own AWG interface, managed automatically via an interface pool.

Used by StealthSurf backend to provision/delete AmneziaWG 2.0 configs on VPN servers.

## Architecture

```text
HTTP API (Bearer auth) → Client Manager (CRUD, IP alloc, keygen)
                        → AWG Pool (multi-interface, per-profile)
                        → AWG Interfaces (kernel module + awg CLI)
                        → JSON Storage (/data/clients.json)
                        → Usage Collector (background, 60s tick)
                        → Usage Storage (/data/usage.json)
```

## Key Directories

- `internal/config/` — Environment-based configuration parsing
- `internal/awg/` — Interface pool, AWG params, Curve25519 keygen, awg CLI helpers
- `internal/clients/` — Client CRUD, IP allocation, JSON persistence
- `internal/api/` — HTTP server, Bearer auth middleware, 7 handlers (5 CRUD + stats + health)
- `internal/usage/` — Background usage collector (rx/tx per peer via `awg show dump`, delta tracking, JSON persistence)
- `internal/update/` — Self-update from GitHub Releases
- `main.go` — Entry point: CLI commands (version, update) → config → pool → manager → usage collector → HTTP → graceful shutdown

## CLI Commands

- `awg-server` — start the server
- `awg-server version` — print version and exit
- `awg-server update` — self-update from latest GitHub release

## Development

```bash
make build VERSION=1.0.0   # Build with version
go vet ./...               # Static analysis
```

## API Endpoints

All require `Authorization: Bearer <AWG_API_TOKEN>` except `/health`.

| Method | Path | Description |
| ------ | ---- | ----------- |
| GET | `/health` | Health check (no auth) |
| GET | `/api/clients` | List all clients |
| POST | `/api/clients` | Create client `{"id":"uuid","awg_params":{...}}` |
| PATCH | `/api/clients/{id}` | Update client `{"awg_params":{...}}` (atomic migration via `MigratePeer`) |
| GET | `/api/clients/{id}/configuration` | Get .conf file |
| GET | `/api/clients/{id}/stats` | Get usage stats (rx/tx bytes, last handshake) |
| DELETE | `/api/clients/{id}` | Delete client |

## Configuration (env vars)

**Required:** `AWG_API_TOKEN`, `AWG_ADDRESS` (CIDR), `AWG_ENDPOINT` (public IP)

**Optional:** `AWG_LISTEN_PORT` (51820), `AWG_HTTP_PORT` (7777), `AWG_MTU` (1420), `AWG_DNS` (1.1.1.1), `AWG_DATA_DIR` (/data), `AWG_INTERFACE` (auto-detect), `AWG_MAX_INTERFACES` (0 = unlimited)

**Auto-generated (first start, persisted in `/data/clients.json`):** `H1`-`H4` (random non-overlapping ranges, format `min-max`), `S1`, `S2` (random 15-150, `S1+56 ≠ S2`)

**Default AmneziaWG params (from env):** `AWG_JC` (5), `AWG_JMIN` (50), `AWG_JMAX` (1000), `AWG_S3` (0), `AWG_S4` (0), `AWG_I1`-`AWG_I5` (client config only)

Clients can override defaults via `awg_params` in API requests.

## Key Patterns

- **Multi-interface pool**: each unique CPS profile = separate awg interface (awg0, awg1, ...)
- Interfaces created on demand, destroyed when last peer is removed
- Each interface listens on its own UDP port (explicit `port` from `awg_params`, or auto-assigned sequentially from `AWG_LISTEN_PORT`)
- **Atomic peer migration**: `MigratePeer` handles CPS profile changes — if client is last peer on old interface, removes first (freeing port), then creates new interface; otherwise adds to new first, then removes from old; port-only change on shared interface rejected (409); best-effort rollback on failure
- All interfaces share the same server private key
- Kernel module approach: `amneziawg-linux-kernel-module` on host, `awg` CLI on host
- Deployed as static binary, no Docker needed
- Bearer token auth on all endpoints
- **Usage tracking**: background collector polls `awg show dump` every 60s, accumulates rx/tx deltas per peer (handles counter resets), persists to `/data/usage.json`
- **Param generation**: H1-H4, S1, S2 generated at first start via `crypto/rand`, persisted alongside server private key
- **Interface grouping** (`Key()`): only H1-H4, S1-S4 determine the interface; Jc/Jmin/Jmax and I1-I5 do NOT create new interfaces
- **I1-I5**: client-side only (CPS packets), included in `.conf` but NOT in `awg set` CLI args
- JSON file persistence with atomic write (tmp + rename)
- IP allocation: sequential from .2, freed IPs reused
- Thread safety: `sync.Mutex` in Pool, `sync.RWMutex` in Manager
- Network config via `ip` / `iptables` commands (requires root / NET_ADMIN)

## Code Style

- Go standard library only for HTTP (`net/http` ServeMux)
- Early returns, short funcs
- Vertical spacing between variable declarations
- No comments unless logic is non-obvious
- English in code, Russian in communication


<claude-mem-context>
# Recent Activity

<!-- This section is auto-generated by claude-mem. Edit content outside the tags. -->

*No recent activity*
</claude-mem-context>