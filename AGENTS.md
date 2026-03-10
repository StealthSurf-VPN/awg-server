# AGENTS.md

## Project Context

This is a Go HTTP API server (`awg-server`) for managing AmneziaWG VPN clients on servers. It uses the **AmneziaWG kernel module** on the host and the `awg` CLI tool for device/peer management, exposing a REST API. Deployed as a static binary directly to VPN servers.

Supports **per-client obfuscation profiles** via a multi-interface pool — each unique set of CPS parameters gets its own AWG interface.

## Agent Guidelines

### Understanding the Codebase

- Read `.claude/CLAUDE.md` for project overview
- Read `.claude/rules/` for coding conventions and architecture
- Read `.claude/docs/` for API and configuration reference
- The entire codebase is in `internal/` with 4 packages: `config`, `awg`, `clients`, `api`

### Key Files

| File | Purpose |
| ---- | ------- |
| `main.go` | Entry point, startup sequence, graceful shutdown |
| `internal/config/config.go` | Environment variable parsing |
| `internal/awg/keygen.go` | Curve25519 key pair generation |
| `internal/awg/params.go` | AWGParams struct (Key, CLIArgs, ConfigLines) |
| `internal/awg/device.go` | AWG interface helpers (create, configure, destroy, peer ops) |
| `internal/awg/pool.go` | Interface pool — multi-interface management |
| `internal/clients/storage.go` | JSON file persistence (atomic write) |
| `internal/clients/manager.go` | Client CRUD, IP allocation, .conf generation |
| `internal/api/server.go` | HTTP server, Bearer auth middleware |
| `internal/api/handlers.go` | 5 API handlers (list, create, update, config, delete) + health |

### Dependency Flow

```text
config ← awg ← clients ← api ← main
```

Never create circular dependencies between packages.

### Making Changes

1. **Adding API endpoints**: Add handler in `handlers.go`, register route in `server.go`
2. **Adding config params**: Add field to `Config` struct, parse env var in `Load()`
3. **Modifying client data**: Update `ClientData` struct in `storage.go`, update manager
4. **Changing AWG behavior**: Modify helpers in `device.go`, pool logic in `pool.go`
5. **Changing CPS params**: Update `AWGParams` struct in `params.go` (Key, CLIArgs, ConfigLines methods)

### Testing

```bash
go build -o awg-server .  # Must compile
go vet ./...               # Must pass
```

### Integration with StealthSurf Backend

This server runs on VPN servers. The NestJS backend calls the API via:

- `GET /health` — health check (no auth, for monitoring)
- `GET /api/clients` — list (for orphan cleanup in `ended-configs-cleaner`)
- `POST /api/clients` — create (when user requests AmneziaWG config, optionally with custom `awg_params`)
- `PATCH /api/clients/{id}` — update (change obfuscation profile)
- `GET /api/clients/{id}/configuration` — get .conf file
- `DELETE /api/clients/{id}` — delete (cleanup, user deletion)

Auth: `Authorization: Bearer <token>` where token is stored in server settings.
