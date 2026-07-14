# Architecture Rules

## Package Responsibilities

- `internal/config` parses and validates configuration without depending on other internal packages.
- `internal/awg` owns keys, CPS parameters, device commands, interface lifecycle, and pool state.
- `internal/clients` owns persisted client records, IP allocation, and configuration generation.
- `internal/usage` polls peer counters and persists accumulated usage.
- `internal/api` translates HTTP requests into manager, pool, and usage operations.
- `internal/update` implements the standalone self-update path.
- `main.go` is the composition root; keep dependency wiring and process lifecycle there.

The allowed dependency direction is defined in `AGENTS.md`. Keep lower-level packages unaware of HTTP concerns.

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
- Server-side obfuscation params via `awg set`: Jc/Jmin/Jmax, S1-S4, H1-H4 — encapsulated in `AWGParams`
- Client-side only: MTU, DNS, PersistentKeepalive, and I1-I5 (`MTU`, DNS, and PersistentKeepalive are rendered by the client manager; I1-I5 are included in `.conf` but not in `awg set`)
- Peer operations via `awg set ... peer`; stats via `awg show ... dump` (used by usage collector)
- Network configuration (IP, routing, NAT) via `exec.Command`
- MASQUERADE rule added once for the subnet, removed on pool close

## AWGParams

- Defined in `internal/awg/params.go`
- `Port` — optional UDP listen port for the interface (not part of CPS, not in Key/CLIArgs/ConfigLines); zero selects automatic assignment, explicit values are validated in the range 1024-65535
- `MTU` — optional client config override, range 1280-1420 (not part of CPS, not in Key/CLIArgs/ConfigLines); zero inherits `AWG_MTU`
- `DNS` — optional client config override containing one IPv4 address (empty inherits `AWG_DNS`; DoH URLs and other formats are rejected; not part of CPS, not in Key/CLIArgs/ConfigLines)
- `PersistentKeepalive` — optional pointer-valued client `[Peer]` override, range 0-65535 (nil inherits 25, explicit zero disables; not part of CPS, not in Key/CLIArgs/ConfigLines)
- `Key()` — deterministic string for interface grouping: **only H1-H4, S1-S4** (excludes Port, MTU, DNS, PersistentKeepalive, Jc/Jmin/Jmax, I1-I5)
- `CLIArgs()` — args for `awg set`: H1-H4, S1-S4, Jc/Jmin/Jmax (excludes I1-I5 — client-only)
- `ConfigLines()` — CPS lines for the client `.conf` `[Interface]` section, including I1-I5; MTU, DNS, and PersistentKeepalive are rendered separately by the client manager
- `GenerateParams()` — generates H1-H4 (random non-overlapping ranges, format `min-max`) and S1, S2 (random 15-150, `S1+56 ≠ S2`)
- Per-client: stored as `*AWGParams` in `ClientData` (nil = use server defaults)
- `ClientData` has `ID` (no separate `Name` field; POST body uses `id` directly)

**Protocol rules:**
- **Must match** server↔client: H1-H4, S1-S4
- **Can differ** server↔client: Jc, Jmin, Jmax, I1-I5
- **I1-I5**: client-side CPS packets, server does not use them in `awg set`
- **DNS**: client-side `[Interface]` behavior; server does not configure it on AWG interfaces
- **PersistentKeepalive**: client-side `[Peer]` behavior; server does not set it on the peer

## Persistence

- **Clients**: `{AWG_DATA_DIR}/clients.json` — server private key, generated AWG params, client data
- **Usage**: `{AWG_DATA_DIR}/usage.json` — accumulated rx/tx per peer (keyed by base64 public key)
- Atomic writes: write to `.tmp`, then `os.Rename`
- Server private key generated once and persisted
- Generated AWG params (H1-H4, S1, S2) generated once at first start and persisted as `generated_params` in clients.json
- Per-client `awg_params` persisted (omitted if nil/default)
- On startup: load JSON → load/generate params → group by effective params → recreate interfaces → re-add peers

## Deployment

- Static binary (`CGO_ENABLED=0`), deployed directly to VPN servers
- Requires: `amneziawg` kernel module, `awg` CLI, `iptables`, `iproute2`
- Runs as root or with `NET_ADMIN` capability
- `net.ipv4.ip_forward=1` sysctl required
- Volume at `/data` for persistence
- Firewall must allow UDP port range (base port through base + max interfaces)
