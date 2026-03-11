# Architecture Rules

## Module Boundaries

```text
main.go
  → internal/config    (no dependencies on other internal packages)
  → internal/awg       (depends on config)
  → internal/clients   (depends on awg, config)
  → internal/api       (depends on clients, awg, config)
```

Dependency flow is one-directional. Never import `api` from `clients` or `awg`.

## Multi-Interface Pool

- AmneziaWG 2.0 CPS parameters are set at the **interface level**, not per-peer
- The `Pool` manages multiple interfaces, one per unique CPS parameter set
- Interface names: `awg0`, `awg1`, `awg2`, ... (sequential)
- Ports: explicit `port` from `AWGParams`, or auto-assigned sequentially from `AWG_LISTEN_PORT` (first available)
- Interfaces created on demand via `ip link add awgN type amneziawg`
- Interfaces destroyed when their last peer is removed
- **Peer migration** (`Pool.MigratePeer`): when client changes CPS profile, if it's the last peer on old interface — remove first to free port, then create new interface (allows reusing same port); if other peers exist — add to new first, then remove from old; port-only change on shared interface rejected (`ErrPortShared`, 409); rollback on failure via `rollbackPeer`
- All interfaces share the same server private key
- `AWG_MAX_INTERFACES` limits total interfaces (0 = unlimited)

## Device Management

- Each interface configured via `awg set` with private key through stdin
- Obfuscation params: Jc/Jmin/Jmax, S1-S4, H1-H4, I1-I5 (CPS) — encapsulated in `AWGParams`
- Peer operations via `awg set ... peer` and `awg show ... dump`
- Network configuration (IP, routing, NAT) via `exec.Command`
- MASQUERADE rule added once for the subnet, removed on pool close

## AWGParams

- Defined in `internal/awg/params.go`
- `Port` — optional UDP listen port for the interface (not part of CPS, not in Key/CLIArgs/ConfigLines); validated range 1024-65535
- `Key()` — deterministic string for CPS profile grouping (excludes port)
- `CLIArgs()` — args for `awg set` (CPS params only)
- `ConfigLines()` — lines for client `.conf` `[Interface]` section (CPS params only)
- Per-client: stored as `*AWGParams` in `ClientData` (nil = use server defaults)
- `ClientData` has `ID` (no separate `Name` field; POST body uses `id` directly)

## Persistence

- Single JSON file at `{AWG_DATA_DIR}/clients.json`
- Atomic writes: write to `.tmp`, then `os.Rename`
- Server private key persisted alongside clients
- Per-client `awg_params` persisted (omitted if nil/default)
- On startup: load JSON → group by effective params → recreate interfaces → re-add peers

## HTTP API

- Standard `net/http` ServeMux (Go 1.22+ method routing)
- Bearer token middleware on all routes (except `/health`)
- `GET /health` — unauthenticated health check for monitoring
- JSON responses for structured data, plain text for .conf files
- Status codes: 200 (list/get/update), 201 (create), 204 (delete), 400 (bad request), 401 (auth), 404 (not found), 409 (conflict/port in use/port shared), 503 (max interfaces)

## Deployment

- Static binary (`CGO_ENABLED=0`), deployed directly to VPN servers
- Requires: `amneziawg` kernel module, `awg` CLI, `iptables`, `iproute2`
- Runs as root or with `NET_ADMIN` capability
- `net.ipv4.ip_forward=1` sysctl required
- Volume at `/data` for persistence
- Firewall must allow UDP port range (base port through base + max interfaces)
