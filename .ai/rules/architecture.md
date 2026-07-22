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
- Ports: explicit `port` from `AWGParams`, or auto-assigned sequentially from `AWG_LISTEN_PORT` (first available). A newly added peer can share an existing profile when its requested port is zero or matches the actual port; a different explicit port is rejected before peer mutation. PATCH rejects any stored port change while that profile is shared, even when switching between zero and the actual port.
- Interfaces created on demand via `ip link add awgN type amneziawg`
- Peer removal uses transaction-style ordering across the AWG peer, its `/32` route, and final interface cleanup. Route deletion or final interface destruction failure attempts best-effort restoration of the peer, route, and bookkeeping. The operation returns an error even when restoration succeeds and cannot promise pristine state if restoration or the process fails.
- **Peer migration** (`Pool.MigratePeer`): when client changes CPS profile, if it's the last peer on old interface — remove first to free port, then create new interface (allows reusing same port); if other peers exist — add to new first, then remove from old; port-only change on shared interface rejected (`ErrPortShared`, 409). A failed shared-interface removal is never success: the pool best-effort restores the old peer and route, removes the new peer, updates bookkeeping for successful rollback steps, and returns an error even when rollback is partial.
- An incompletely configured kernel interface that also fails immediate deletion is quarantined separately from working profiles: its port remains reserved, it counts toward the interface limit, it is excluded from usage collection, and `Pool.Close` retries deletion.
- All interfaces share the same server private key
- `AWG_MAX_INTERFACES` limits total interfaces (0 = unlimited)

## Device Management

- Each interface configured via `awg set` with private key through stdin
- Server-side obfuscation params via `awg set`: Jc/Jmin/Jmax, S1-S4, H1-H4 — encapsulated in `AWGParams`
- Client-side only: ClientListenPort, MTU, DNS, PersistentKeepalive, and I1-I5 (`ClientListenPort`, MTU, DNS, and PersistentKeepalive are rendered by the client manager; I1-I5 are included in `.conf` but not in `awg set`)
- Peer operations via `awg set ... peer`; optional per-peer PSKs are passed through stdin using `preshared-key /dev/stdin`; stats via `awg show ... dump` (used by usage collector)
- Network configuration (IP, routing, NAT) via `exec.Command`
- MASQUERADE rule added once for the subnet. `Pool.Close` removes it only after every working and quarantined interface was destroyed successfully; cleanup failures leave it in place and are logged for operator recovery.
- `AWG-LAN` is rebuilt atomically with `iptables-restore --wait 5 --noflush`. The rule-1 `FORWARD` hook matches only VPN-subnet traffic between `awg+` interfaces; same-group address pairs are accepted and the chain otherwise drops.

## AWGParams

- Defined in `internal/awg/params.go`
- `Port` — optional UDP listen port for the interface (not part of CPS, not in Key/CLIArgs/ConfigLines); zero selects automatic assignment, explicit values are validated in the range 1024-65535
- `ClientListenPort` — optional local UDP listen port for the generated client `[Interface]`, range 1024-65535 (zero omits `ListenPort` for automatic client-side selection; not part of CPS, Key, CLIArgs, ConfigLines, server interface allocation, or peer migration)
- `MTU` — optional client config override, range 1280-1420 (not part of CPS, not in Key/CLIArgs/ConfigLines); zero inherits `AWG_MTU`
- `DNS` — legacy optional client config override containing one IPv4 address (empty inherits `AWG_DNS`; cannot be combined with mode-based DNS; not part of CPS, Key, CLIArgs, or ConfigLines)
- `DNSMode` / `DNSServers` — explicit client DNS selection: `default` inherits `AWG_DNS`, `custom` renders a normalized IPv4 list, and `system` omits the DNS line. Presence validation follows case-insensitive JSON field matching; all DNS fields stay outside CPS and interface grouping.
- `PersistentKeepalive` — optional pointer-valued client `[Peer]` override, range 0-65535 (nil inherits 25, explicit zero disables; not part of CPS, not in Key/CLIArgs/ConfigLines)
- `Key()` — deterministic string for interface grouping: **only H1-H4, S1-S4** (excludes Port, ClientListenPort, MTU, all DNS fields, PersistentKeepalive, Jc/Jmin/Jmax, I1-I5)
- `CLIArgs()` — args for `awg set`: H1-H4, S1-S4, Jc/Jmin/Jmax (excludes I1-I5 — client-only)
- `ConfigLines()` — CPS lines for the client `.conf` `[Interface]` section, including I1-I5; ClientListenPort, MTU, all DNS fields, and PersistentKeepalive are rendered separately by the client manager
- `GenerateParams()` — generates H1-H4 (random non-overlapping ranges, format `min-max`) and S1, S2 (random 15-150, `S1+56 ≠ S2`)
- `ValidateOverrides()` validates raw API values before inheritance so invalid negative or malformed values cannot disappear during the merge
- `ValidateProfile()` validates the complete effective profile, including cross-field J/S relationships and H-range overlap
- Per-client: stored as `*AWGParams` in `ClientData` (nil = use server defaults)
- `ClientData` has `ID` (no separate `Name` field; POST body uses `id` directly)
- `ClientData.PresharedKey` is a server-generated per-peer secret, not an `AWGParams` field and never part of interface grouping
- `ClientData.LANGroupID` is persisted and controls only server-side inter-client firewall membership; missing legacy values become `peer:<id>`

**Protocol rules:**
- **Must match** server↔client: H1-H4, S1-S4
- **Can differ** server↔client: Jc, Jmin, Jmax, I1-I5
- **I1-I5**: client-side CPS packets, server does not use them in `awg set`
- **ClientListenPort**: client-side `[Interface]` behavior; server does not reserve the port, pass it to `awg set`, or use it in `Endpoint`
- **DNS**: client-side `[Interface]` behavior; server does not configure it on AWG interfaces
- **PersistentKeepalive**: client-side `[Peer]` behavior; server does not set it on the peer

Create and update operations validate raw overrides and the effective profile before key generation, IP allocation, peer migration, or persistence. `main.go` validates the default profile before creating the interface pool. Persisted clients pass the same AWG and routing validation during fail-fast restoration; invalid records abort startup instead of being silently discarded.

## Client Routing

- Per-client routing is stored as `*clients.Routing`; nil means full tunnel for backward compatibility.
- Every mode explicitly prepends the configured VPN network. After it, `full` renders `0.0.0.0/0, ::/0`; `bypass` subtracts `excluded_ips` from all IPv4 routes and retains `::/0`; `split` renders normalized `allowed_ips` minus optional `excluded_ips` without implicit IPv6.
- Client routing never participates in `AWGParams`, interface grouping, port allocation, peer migration, or server-side peer `allowed-ips`.
- Domain, application, and geosite routing require client-side logic and are not part of the generated AWG configuration contract.

## Persistence

- **Clients**: `{AWG_DATA_DIR}/clients.json` — server private key, generated AWG params, client data
- **Usage**: `{AWG_DATA_DIR}/usage.json` — accumulated rx/tx per peer (keyed by base64 public key)
- Replace-style writes: write to `.tmp`, then `os.Rename`; this does not make a kernel/filesystem operation absolutely atomic or guarantee durability across every crash
- Server private key generated once and persisted
- Generated AWG params (H1-H4, S1, S2) generated once at first start and persisted as `generated_params` in clients.json
- Per-client `awg_params` persisted (omitted if nil/default)
- Per-client `routing` persisted for bypass and split policies; nil/full is omitted for backward compatibility
- New clients receive a unique 32-byte PSK persisted as `preshared_key`; legacy records may omit it
- Every client has a persisted `lan_group_id`; create defaults it to `peer:<id>`, and startup saves the same unique default for legacy records
- Create, delete, and LAN-group mutation install an empty DROP-only `AWG-LAN` while holding the manager lock, then save/commit membership and rebuild same-group allows. A later error leaves a LAN outage instead of restoring permissive rules.
- Create stages device state, saves prospective JSON, then commits memory. Client-only update saves before committing memory; interface update/regeneration migrates before saving; delete removes device state before saving. Later failures trigger best-effort device rollback and a generic API `500`, but rollback can itself fail.
- On startup: load JSON → load/generate params → validate each client keypair, PSK, AWG settings, and routing → group by effective params → recreate interfaces → re-add peers. Non-empty persisted clients require the existing top-level server private key and generated H/S defaults; missing values, invalid persisted state, or any peer restoration failure abort startup without silently generating replacement state or dropping that client. Already-created pool state is closed best-effort.

## Usage Collection

- Periodic, manual, and required pre-migration collections serialize the complete interface-list, dump, and in-memory counter update sequence with one collector guard.
- Every interface-level PATCH and per-client H/S regeneration acquires the manager write lock first, then the collector takes a complete final snapshot and holds its guard through migration, client persistence, and any reverse migration; the callback wiring from `api` preserves package dependency direction.
- If any interface dump command fails, contains a malformed peer row, or returns no peers for an active interface during the required snapshot, the update aborts before pool mutation. Detailed dump errors stay in usage logs; the manager and HTTP boundary receive only a safe snapshot error and return generic `500`.
- The required snapshot updates in-memory totals but does not immediately save `usage.json`; normal saves follow startup and each 60-second collection, with a final collect/save during graceful shutdown.

## Deployment

- Static binary (`CGO_ENABLED=0`), deployed directly to VPN servers
- Requires: `amneziawg` kernel module, `awg` CLI, `iptables`, `iproute2`
- Runs as root or with `NET_ADMIN` capability
- `net.ipv4.ip_forward=1` sysctl required
- Volume at `/data` for persistence
- Firewall must allow the automatic UDP port range and every explicit per-client interface port
