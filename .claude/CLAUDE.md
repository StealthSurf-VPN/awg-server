# CLAUDE.md - AmneziaWG Server

## Project Overview

**awg-server** is a Go HTTP API server for managing **AmneziaWG 2.0** VPN clients. It uses the **AmneziaWG kernel module** on the host and the `awg` CLI tool for device and peer management, providing near-native WireGuard performance with DPI obfuscation via CPS (Custom Protocol Signature).

Used by StealthSurf backend to provision/delete AmneziaWG 2.0 configs on VPN servers.

## Architecture

```text
HTTP API (Bearer auth) → Client Manager (CRUD, IP alloc, keygen)
                        → AWG Device (kernel module + awg CLI)
                        → JSON Storage (/data/clients.json)
```

## Key Directories

- `internal/config/` — Environment-based configuration parsing
- `internal/awg/` — AmneziaWG device lifecycle (awg CLI), Curve25519 keygen
- `internal/clients/` — Client CRUD, IP allocation, JSON persistence
- `internal/api/` — HTTP server, Bearer auth middleware, 4 API handlers
- `main.go` — Entry point: config → device → manager → HTTP → graceful shutdown

## Development

```bash
go build -o awg-server .  # Build binary
go vet ./...               # Static analysis
```

## API Endpoints

All require `Authorization: Bearer <AWG_API_TOKEN>`.

| Method | Path | Description |
| ------ | ---- | ----------- |
| GET | `/api/clients` | List all clients |
| POST | `/api/clients` | Create client `{"name":"uuid"}` |
| GET | `/api/clients/{id}/configuration` | Get .conf file |
| DELETE | `/api/clients/{id}` | Delete client |

## Configuration (env vars)

**Required:** `AWG_API_TOKEN`, `AWG_ADDRESS` (CIDR), `AWG_ENDPOINT` (public IP)

**Optional:** `AWG_LISTEN_PORT` (51820), `AWG_HTTP_PORT` (7777), `AWG_MTU` (1420), `AWG_DNS` (1.1.1.1), `AWG_DATA_DIR` (/data)

**AmneziaWG params:** `AWG_JC`, `AWG_JMIN`, `AWG_JMAX`, `AWG_S1`-`AWG_S4`, `AWG_H1`-`AWG_H4`, `AWG_I1`-`AWG_I5` (CPS)

## Key Patterns

- Kernel module approach: `amneziawg-linux-kernel-module` on host, `awg` CLI on host
- Deployed as static binary, no Docker needed
- Bearer token auth on all endpoints
- JSON file persistence with atomic write (tmp + rename)
- IP allocation: sequential from .2, freed IPs reused
- Thread safety: `sync.RWMutex` around client operations
- Network config via `ip` / `iptables` commands (requires root / NET_ADMIN)

## Code Style

- Go standard library only for HTTP (`net/http` ServeMux)
- Early returns, short funcs
- Vertical spacing between variable declarations
- No comments unless logic is non-obvious
- English in code, Russian in communication
