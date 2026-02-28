# AGENTS.md

## Project Context

This is a Go HTTP API server (`awg-server`) for managing AmneziaWG VPN clients on servers. It uses the **AmneziaWG kernel module** on the host and the `awg` CLI tool for device/peer management, exposing a REST API. Deployed as a static binary directly to VPN servers.

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
| `internal/awg/device.go` | AmneziaWG device lifecycle (kernel module + awg CLI) |
| `internal/clients/storage.go` | JSON file persistence (atomic write) |
| `internal/clients/manager.go` | Client CRUD, IP allocation, .conf generation |
| `internal/api/server.go` | HTTP server, Bearer auth middleware |
| `internal/api/handlers.go` | 4 API handlers (list, create, config, delete) |

### Dependency Flow

```text
config ← awg ← clients ← api ← main
```

Never create circular dependencies between packages.

### Making Changes

1. **Adding API endpoints**: Add handler in `handlers.go`, register route in `server.go`
2. **Adding config params**: Add field to `Config` struct, parse env var in `Load()`
3. **Modifying client data**: Update `ClientData` struct in `storage.go`, update manager
4. **Changing AWG behavior**: Modify `awg` CLI calls in `device.go`

### Testing

```bash
go build -o awg-server .  # Must compile
go vet ./...               # Must pass
```

### Integration with StealthSurf Backend

This server runs on VPN servers. The NestJS backend calls the API via:

- `GET /api/clients` — list (for orphan cleanup in `ended-configs-cleaner`)
- `POST /api/clients` — create (when user requests AmneziaWG config)
- `GET /api/clients/{id}/configuration` — get .conf file
- `DELETE /api/clients/{id}` — delete (cleanup, user deletion)

Auth: `Authorization: Bearer <token>` where token is stored in server settings.
