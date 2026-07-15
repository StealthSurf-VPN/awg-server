# Configuration Reference

All configuration is done via environment variables.

## Required

| Variable | Description | Example |
| -------- | ----------- | ------- |
| `AWG_API_TOKEN` | Bearer token for API auth | `my-secret-token-123` |
| `AWG_ADDRESS` | Server VPN address (CIDR) | `10.0.0.1/24` |
| `AWG_ENDPOINT` | Public IP/hostname for clients | `vpn.example.com` |

## Optional

| Variable | Default | Description |
| -------- | ------- | ----------- |
| `AWG_LISTEN_PORT` | `51820` | Base WireGuard UDP listen port. Auto-assigned interfaces use the first free port from this value upward. Per-client `port` can request an explicit 1024-65535 port. |
| `AWG_HTTP_PORT` | `7777` | HTTP API listen port |
| `AWG_MTU` | `1420` | Default MTU for client configs. Can be overridden per-client with `mtu` in `awg_params`. |
| `AWG_DNS` | `1.1.1.1` | Default DNS server for client configs. Inherited when DNS fields are omitted or `awg_params.dns_mode` is `default`; `system` mode omits the client `DNS` line. |
| `AWG_DATA_DIR` | `/data` | Directory for clients.json persistence |
| `AWG_INTERFACE` | auto-detect | Override outbound network interface for MASQUERADE (default: auto-detected from default route) |
| `AWG_MAX_INTERFACES` | `0` | Maximum number of AWG interfaces. 0 = unlimited. Returns 503 when exceeded. |

## Auto-Generated Parameters

On first start, the server generates and persists unique obfuscation values in `{AWG_DATA_DIR}/clients.json`:

- **H1-H4** — random non-overlapping ranges, format `min-max` (header masking)
- **S1, S2** — random 15-150, with constraint `S1 + 56 ≠ S2` (handshake padding)

These server-wide defaults are reused across restarts. They are distinct from H1-H4/S1-S2 stored in an individual client's `awg_params`: per-client generation or regeneration never changes `generated_params`. No env vars are needed.

## Per-Client Preshared Keys

Every newly created client receives an independent 32-byte WireGuard preshared key generated with `crypto/rand`. The key is stored as `preshared_key` in `{AWG_DATA_DIR}/clients.json`, installed on the corresponding AWG peer, and included in the generated client configuration. It is not configurable through an environment variable or accepted from the API.

Existing client records without `preshared_key` remain valid and continue without PSK. The server does not generate keys for them automatically because their already-issued configurations would no longer connect.

## Default AmneziaWG Obfuscation Parameters

These env vars set **default** CPS parameters for clients that don't specify custom `awg_params` via the API.

| Variable | Default | Description |
| -------- | ------- | ----------- |
| `AWG_JC` | `5` | Junk packet count |
| `AWG_JMIN` | `50` | Junk packet minimum size |
| `AWG_JMAX` | `1000` | Junk packet maximum size |
| `AWG_S3` | `0` | Underload packet padding |
| `AWG_S4` | `0` | Transport packet padding |
| `AWG_I1`-`AWG_I5` | empty | CPS signature packets (client config only) |

Parameters with value `0` are omitted from client configs and `awg set` commands. **Exception:** `S3`/`S4` are always emitted (even when 0).

Clients can override defaults by providing `awg_params` in the create/update API request. Per-client params are merged over defaults (non-zero values override unless documented otherwise). The optional `port` field accepts 1024-65535 and uses automatic server interface assignment when omitted or zero. The optional `client_listen_port` field accepts 1024-65535 and adds `ListenPort` to the generated client `[Interface]`; omission or zero leaves port selection automatic. It has no global environment default and does not affect the server-side interface port. The optional `mtu` field accepts 1280-1420 and inherits `AWG_MTU` when omitted or zero. Legacy `dns` accepts one IPv4 address and inherits `AWG_DNS` when omitted or empty. Mode-based DNS inherits `AWG_DNS` when `dns_mode` is `default`, joins `dns_servers` for `custom`, and omits the complete `DNS` line for `system`. The optional `persistent_keepalive` field accepts 0-65535, inherits 25 when omitted, and is disabled by an explicit zero. See the [`awg_params` API contract](api.md#dns-settings) for DNS combinations and validation.

The complete default profile, including persisted generated H/S values and CPS environment values, is validated before the interface pool starts. Invalid defaults prevent startup and produce a field-specific log error. The accepted J/S/H/I constraints are the same as the [`awg_params` API contract](api.md#awg-params-object).

## Multi-Interface Behavior

When clients have different CPS parameters, the server creates separate AWG interfaces:

- Interface grouping key: H1-H4, S1-S4 only. All DNS fields (`dns`, `dns_mode`, and `dns_servers`) and all routing fields (`mode`, `allowed_ips`, and `excluded_ips`) stay outside interface grouping, as do client listen port, MTU, PersistentKeepalive, Jc/Jmin/Jmax, and I1-I5.
- Each unique parameter set gets its own `awgN` interface (awg0, awg1, ...)
- Each interface listens on the explicit `port` from `awg_params`, or auto-assigned sequentially from `AWG_LISTEN_PORT`
- When a grouping profile already exists, a newly added peer can join it with port 0 or the interface's actual port; another explicit port returns `409` before peer mutation. PATCH rejects any stored port change while that profile has multiple peers.
- Interfaces are created on demand and destroyed when empty
- `AWG_MAX_INTERFACES` limits the total number of interfaces

Ensure your firewall allows the automatic range and every explicit per-client UDP port that will be used. An explicit port is not constrained to a small range above `AWG_LISTEN_PORT`.

## Persistence

Client data is stored in `{AWG_DATA_DIR}/clients.json`. The top-level `generated_params` object is the server-wide H/S default generated on first startup:

```json
{
  "server_private_key": "<base64>",
  "generated_params": {
    "h1": "234567-678901",
    "h2": "2345678-6789012",
    "h3": "23456789-67890123",
    "h4": "234567890-678901234",
    "s1": 42,
    "s2": 87
  },
  "clients": [
    {
      "id": "uuid",
      "private_key": "<base64>",
      "public_key": "<base64>",
      "preshared_key": "<base64>",
      "address": "10.0.0.2",
      "created_at": "2026-01-01T00:00:00Z"
    }
  ]
}
```

The pure `POST /api/awg-params/generate` endpoint does not persist its result. Per-client H/S regeneration writes overrides under that client's `awg_params` and leaves the top-level `generated_params` unchanged. A client with custom DNS, regenerated H/S values, and bypass routing can contain this fragment:

```json
{
  "awg_params": {
    "dns_mode": "custom",
    "dns_servers": ["1.1.1.1", "1.0.0.1"],
    "h1": "234567-678901",
    "h2": "2345678-6789012",
    "h3": "23456789-67890123",
    "h4": "234567890-678901234",
    "s1": 42,
    "s2": 87
  },
  "routing": {
    "mode": "bypass",
    "excluded_ips": ["10.0.0.0/8", "192.168.0.0/16"]
  }
}
```

Combined split routing persists the normalized included and excluded intent, not the expanded computed CIDRs:

```json
{
  "routing": {
    "mode": "split",
    "allowed_ips": ["10.0.0.0/8", "172.16.0.0/12"],
    "excluded_ips": ["10.20.0.0/16"]
  }
}
```

Legacy DNS records retain their original shape, for example `{"awg_params":{"dns":"9.9.9.9"}}`. A standalone empty legacy override (`{"dns":""}`) is canonically equivalent to no AWG override and is omitted when that client is next created or updated. Explicit `default` and `system` modes are stored as `dns_mode`, while custom mode also stores its normalized, stably deduplicated `dns_servers`. Existing legacy records are not rewritten solely because the server is upgraded.

Clients without custom parameters have `awg_params` omitted and use automatic client listen-port selection plus server defaults, including `AWG_DNS` and `PersistentKeepalive = 25`. Omitted `routing` means full tunnel for backward compatibility; an explicit `{"mode":"full"}` is canonically persisted by omitting `routing`. Bypass and split lists are persisted after network masking and stable exact-deduplication. Clients created before PSK support can also omit `preshared_key`. On startup, all clients are restored and interfaces are recreated as needed, using the persisted PSK when present.

### Client transaction and startup behavior

`clients.json` is written to `clients.json.tmp` with mode `0600` and then renamed into place. API operations coordinate that write with device state:

- create stages the interface, peer, and route, saves the prospective JSON, then commits the in-memory client;
- a client-only update saves first and then replaces the in-memory record;
- an interface-level update or regeneration migrates the peer, saves, then commits memory; a save failure attempts a reverse migration;
- delete removes the peer, route, and now-empty interface, saves, then removes the in-memory client; a save failure attempts to add the peer back.

Device failures and persistence failures return a generic `500 Internal Server Error`. These sequences prevent a normal failed save from being reported as success, but they are not an absolute transaction across the filesystem and kernel. A rollback can fail, and temporary-file rename is not a promise of durability across every host crash. Inspect logs and live interfaces after any device, persistence, or rollback error.

Startup restoration is fail-fast. If `clients` is non-empty, missing top-level `server_private_key` or `generated_params` aborts before generating or rewriting either value. An invalid persisted private, public, or preshared key, a private/public key mismatch, invalid AWG/routing settings, or any client that cannot be re-added to its interface aborts manager construction, prevents the HTTP server from starting, and triggers best-effort pool cleanup. The server no longer silently drops an unrestorable client from the loaded set. A failure to bind the configured HTTP port also terminates the process with a non-zero status after cleanup instead of leaving a process running without an API listener.

### Usage persistence

Accumulated counters are stored separately in `{AWG_DATA_DIR}/usage.json`, also through a mode-`0600` temporary file and rename. The collector polls every active AWG interface once on startup and every 60 seconds, saving after each collection; graceful shutdown performs one final collect and save. The file must be a JSON object with non-null peer entries and non-negative counters. Invalid usage data is logged and ignored in favor of a fresh empty in-memory map, without preventing the client API from starting. `GET /api/clients/{id}/stats` reads the latest in-memory totals and does not force a new poll, so normal responses can lag kernel counters by up to one interval.

Every interface-level PATCH and per-client H/S regeneration takes a required complete snapshot of every active interface before migration. A failed dump command, malformed peer row, or active interface returning no peers makes the snapshot incomplete and aborts the update before mutation. A valid snapshot updates memory immediately and prevents removal of the old peer before its current counters are accumulated, but it does not itself save `usage.json`. The data reaches disk on the next scheduled or graceful-shutdown save; a crash can lose the latest unsaved interval.
