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

- Server-applied AmneziaWG 2.0 and 3.1 settings are set at the **interface
  level**, not per-peer. Client-only settings remain per-client configuration.
- The `Pool` manages multiple interfaces, one per immutable `ProfileKey`, not
  one per `AWGParams.Key()` string. Protocol version and private 3.1
  header-protection key keep otherwise similar 2.0/3.1 profiles separate.
- Interface names: `awg0`, `awg1`, `awg2`, ... (sequential)
- Ports: explicit `port` from `AWGParams`, or auto-assigned sequentially from `AWG_LISTEN_PORT` (first available). A newly added peer can share an existing profile when its requested port is zero or matches the actual port; a different explicit port is rejected before peer mutation. PATCH rejects any stored port change while that profile is shared, even when switching between zero and the actual port.
- Interfaces created on demand via `ip link add awgN type amneziawg`
- Peer removal uses transaction-style ordering across the AWG peer, its `/32` route, and final interface cleanup. Route deletion or final interface destruction failure attempts best-effort restoration of the peer, route, and bookkeeping. The operation returns an error even when restoration succeeds and cannot promise pristine state if restoration or the process fails.
- **Peer migration** (`Pool.MigratePeer`): when client changes CPS profile, if it's the last peer on old interface — remove first to free port, then create new interface (allows reusing same port); if other peers exist — add to new first, then remove from old; port-only change on shared interface rejected (`ErrPortShared`, 409). A failed shared-interface removal is never success: the pool best-effort restores the old peer and route, removes the new peer, updates bookkeeping for successful rollback steps, and returns an error even when rollback is partial.
- An incompletely configured kernel interface that also fails immediate deletion is quarantined separately from working profiles: its port remains reserved, it counts toward the interface limit, it is excluded from usage collection, and `Pool.Close` retries deletion.
- All interfaces share the same server private key
- `AWG_MAX_INTERFACES` limits total interfaces (0 = unlimited)

## Device Management

- Each interface is configured with `awg setconf <interface> /dev/stdin`.
  Pass one complete `Profile.ServerConfig` through stdin; never put server or
  header-protection keys in argv or a temporary file.
- Server-side profile settings include Jc/Jmin/Jmax, S1-S4, H1-H4, and all
  3.1 range/toggle fields. `HeaderProtectionKey` is included only for 3.1.
- Client-side only: ClientListenPort, MTU, DNS, PersistentKeepalive, and I1-I5
  (`ClientListenPort`, MTU, DNS, and PersistentKeepalive are rendered by the
  client manager; I1-I5 are included in `.conf` but not in server setconf).
- Peer operations via `awg set ... peer`; optional per-peer PSKs are passed through stdin using `preshared-key /dev/stdin`; stats via `awg show ... dump` (used by usage collector)
- Network configuration (IP, routing, NAT) via `exec.Command`
- MASQUERADE rule added once for the subnet. `Pool.Close` removes it only after every working and quarantined interface was destroyed successfully; cleanup failures leave it in place and are logged for operator recovery.
- `AWG-LAN` is rebuilt atomically with `iptables-restore --wait 5 --noflush`. The rule-1 `FORWARD` hook matches only VPN-subnet traffic between `awg+` interfaces; same-group address pairs are accepted and the chain otherwise drops.

## AWGParams

- Defined in `internal/awg/params.go`
- `Port` — optional UDP listen port for the interface (not part of CPS, not in Key/CLIArgs/ConfigLines); zero selects automatic assignment, explicit values are validated in the range 1024-65535
- `ClientListenPort` — optional local UDP listen port for the generated client `[Interface]`, range 1024-65535 (zero omits `ListenPort` for automatic client-side selection; not part of CPS, Key, CLIArgs, ConfigLines, server interface allocation, or peer migration)
- `MTU` — optional client config override, range 1280-1420 (not part of
  server profile identity); zero inherits `AWG_MTU` for 2.0 or `AWG31_MTU` for
  3.1.
- `DNS` — legacy optional client config override containing one IPv4 address (empty inherits `AWG_DNS`; cannot be combined with mode-based DNS; not part of CPS, Key, CLIArgs, or ConfigLines)
- `DNSMode` / `DNSServers` — explicit client DNS selection: `default` inherits `AWG_DNS`, `custom` renders a normalized IPv4 list, and `system` omits the DNS line. Presence validation follows case-insensitive JSON field matching; all DNS fields stay outside CPS and interface grouping.
- `PersistentKeepalive` — optional pointer-valued client `[Peer]` override.
  2.0 accepts a scalar 0-65535 (nil inherits 25); 3.1 accepts a strict
  unsigned-16 scalar/range/`off`; it remains outside server profile identity.
- `Key()` remains a legacy H/S helper and is **not** pool identity. Use the
  immutable `Profile` / opaque `ProfileKey`, which includes protocol version,
  all server-applied J/H/S/3.1 fields, and private 3.1 header key while
  excluding client-only fields and requested port.
- `CLIArgs()` is retained for legacy helper use. Server interfaces are
  configured from `Profile.ServerConfig` via `awg setconf` rather than direct
  `awg set` arguments.
- `ConfigLines()` — CPS lines for the client `.conf` `[Interface]` section, including I1-I5; ClientListenPort, MTU, all DNS fields, and PersistentKeepalive are rendered separately by the client manager
- `GenerateParams()` is the legacy 2.0 generator: random non-overlapping H
  ranges and S1/S2. `GenerateParamsV31()` uses fixed unique H values, S1/S2 in
  the supported range, S3 15-63, and S4 12.
- `ValidateOverridesForVersion()` validates raw API values after target-version
  resolution. 2.0 rejects 3.1-only fields and ranged/`off` keepalive;
  `ValidateProfileForVersion()` applies complete effective-profile constraints,
  including 3.1 S1-S4 >= 12 and header-key requirements.
- Per-client public overrides are stored as `*AWGParams` in `ClientData`
  (nil = use target-version defaults). The version and private key reference
  are separate state, not public `AWGParams` fields.
- `ClientData` has `ID` (no separate `Name` field; POST body uses `id` directly)
- `ClientData.PresharedKey` is a server-generated per-peer secret, not an `AWGParams` field and never part of interface grouping
- `ClientData.LANGroupID` is persisted and controls only server-side inter-client firewall membership; missing legacy values become `peer:<id>`

**Protocol rules:**
- **Must match** server↔client: H1-H4, S1-S4
- **Can differ** server↔client: Jc, Jmin, Jmax, I1-I5
- **3.1 must match**: the header-protection key and server-applied 3.1
  range/toggle fields. PersistentKeepalive and I1-I5 remain client-side.
- **I1-I5**: client-side CPS packets, server does not use them in `awg set`
- **ClientListenPort**: client-side `[Interface]` behavior; server does not reserve the port, pass it to `awg set`, or use it in `Endpoint`
- **DNS**: client-side `[Interface]` behavior; server does not configure it on AWG interfaces
- **PersistentKeepalive**: client-side `[Peer]` behavior; server does not set it on the peer

Create and update operations validate raw overrides and the effective profile
before key generation, IP allocation, peer migration, or persistence.
`main.go` validates both target-version defaults before creating the interface
pool. Persisted clients pass the same AWG and routing validation during
fail-fast restoration; invalid records abort startup instead of being silently
discarded.

## Client Routing

- Per-client routing is stored as `*clients.Routing`; nil means full tunnel for backward compatibility.
- Every mode explicitly prepends the configured VPN network. After it, `full` renders `0.0.0.0/0, ::/0`; `bypass` subtracts `excluded_ips` from all IPv4 routes and retains `::/0`; `split` renders normalized `allowed_ips` minus optional `excluded_ips` without implicit IPv6.
- Client routing never participates in `AWGParams`, interface grouping, port allocation, peer migration, or server-side peer `allowed-ips`.
- Domain, application, and geosite routing require client-side logic and are not part of the generated AWG configuration contract.

## Persistence

- **Clients**: `{AWG_DATA_DIR}/clients.json` — server private key, legacy 2.0
  generated params, private `awg_31` state, and client data.
- **Usage**: `{AWG_DATA_DIR}/usage.json` — accumulated rx/tx per peer (keyed by base64 public key)
- Replace-style writes: write to `.tmp`, then `os.Rename`; this does not make a kernel/filesystem operation absolutely atomic or guarantee durability across every crash
- Server private key generated once and persisted
- Legacy generated AWG params (H1-H4, S1, S2) are generated once at first
  start and persisted as `generated_params`; they are never reinterpreted as
  3.1 state.
- `awg_31` holds 3.1 generated H1-H4/S1-S4, a default opaque header-key ID,
  and private base64 header keys. `HeaderProtectionKey` is required and
  non-zero for every 3.1 profile; it has no public DTO or normal JSON response.
- Every client persists canonical `protocol_version` `2.0` or `3.1`. Missing
  legacy disk versions resolve only to 2.0; API alias `2` is normalized at the
  boundary and must never be written to disk.
- Per-client `awg_params` persisted (omitted if nil/default)
- Per-client `routing` persisted for bypass and split policies; nil/full is omitted for backward compatibility
- New clients receive a unique 32-byte PSK persisted as `preshared_key`; legacy records may omit it
- Every client has a persisted `lan_group_id`; create defaults it to `peer:<id>`, and startup saves the same unique default for legacy records
- Create, delete, and LAN-group mutation install an empty DROP-only `AWG-LAN` while holding the manager lock, then save/commit membership and rebuild same-group allows. A later error leaves a LAN outage instead of restoring permissive rules.
- Create stages device state, saves prospective JSON, then commits memory. Client-only update saves before committing memory; interface update/regeneration migrates before saving; delete removes device state before saving. Later failures trigger best-effort device rollback and a generic API `500`, but rollback can itself fail.
- On startup: load JSON → prepare pending defaults only for a state with no
  3.1 client → validate a complete restore plan (keys, versions, references,
  profiles, routing, ports, limits) without client-owned device work → qualify
  the AWG 3.1 runtime → create pool/firewall state and restore peers → save
  one normalization only after successful restore. A persisted 3.1 client with
  missing/invalid private state aborts startup without replacement generation
  or fallback. Non-empty persisted clients require existing top-level server
  private key and legacy generated defaults. On any restore/normalization
  failure, close already-created pool state best-effort and do not start HTTP.
- Mutations deep-copy private key maps. Mark-and-sweep removes only unreferenced
  non-default 3.1 keys from prospective state before Save; successful Save
  commits that map and an unsuccessful save leaves the original map
  authoritative.

## Usage Collection

- Periodic, manual, and required pre-migration collections serialize the complete interface-list, dump, and in-memory counter update sequence with one collector guard.
- Every interface-level PATCH, protocol migration, and per-client regeneration
  acquires the manager write lock first, then the collector takes a complete
  final snapshot and holds its guard through migration, client persistence, and
  any reverse migration; the callback wiring from `api` preserves package
  dependency direction.
- If any interface dump command fails, contains a malformed peer row, or returns no peers for an active interface during the required snapshot, the update aborts before pool mutation. Detailed dump errors stay in usage logs; the manager and HTTP boundary receive only a safe snapshot error and return generic `500`.
- The required snapshot updates in-memory totals but does not immediately save `usage.json`; normal saves follow startup and each 60-second collection, with a final collect/save during graceful shutdown.

## Deployment

- Static binary (`CGO_ENABLED=0`), deployed directly to VPN servers
- Requires the qualified AmneziaWG 3.1 package/module/`awg` runtime,
  `iptables`, and `iproute2`. `awg.CheckRuntime` is the single functional
  capability probe; normal startup and the staged installer binary both use it.
- Runs as root or with `NET_ADMIN` capability
- `net.ipv4.ip_forward=1` sysctl required
- Volume at `/data` for persistence
- Firewall must allow the automatic UDP port range and every explicit per-client interface port
- The installer is the supported 2.0-host migration because self-update cannot
  update/reload DKMS. It installs/gates packages, stages a signed release, then
  stops the service, backs up environment plus clients/usage JSON, refuses any
  remaining AWG interface, reloads, qualifies the staged binary, then
  confirms automatic startup is disabled before replacement, starts the new
  unit explicitly while disabled, verifies health plus an authenticated
  client-list JSON array, and enables it only after those gates pass. Every
  failure after the service stop must preserve or truthfully report both the
  runtime-stop and reboot-time disablement state. Do not add automatic
  rollback/restart after a failed post-replacement gate.
