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
| `AWG_LISTEN_PORT` | `51820` | Base WireGuard UDP listen port. Auto-assigned interfaces use port+1, port+2, etc. Can be overridden per-client via `port` in `awg_params`. |
| `AWG_HTTP_PORT` | `7777` | HTTP API listen port |
| `AWG_MTU` | `1420` | Default MTU for client configs. Can be overridden per-client with `mtu` in `awg_params`. |
| `AWG_DNS` | `1.1.1.1` | Default DNS server for client configs. Can be overridden per-client with one IPv4 address in `awg_params.dns`. |
| `AWG_DATA_DIR` | `/data` | Directory for clients.json persistence |
| `AWG_INTERFACE` | auto-detect | Override outbound network interface for MASQUERADE (default: auto-detected from default route) |
| `AWG_MAX_INTERFACES` | `0` | Maximum number of AWG interfaces. 0 = unlimited. Returns 503 when exceeded. |

## Auto-Generated Parameters

On first start, the server generates and persists unique obfuscation values in `{AWG_DATA_DIR}/clients.json`:

- **H1-H4** — random non-overlapping ranges, format `min-max` (header masking)
- **S1, S2** — random 15-150, with constraint `S1 + 56 ≠ S2` (handshake padding)

These are reused across restarts. No env vars needed.

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

Clients can override defaults by providing `awg_params` in the create/update API request. Per-client params are merged over defaults (non-zero values override unless documented otherwise). The optional `port` field accepts 1024-65535 and uses automatic server interface assignment when omitted or zero. The optional `client_listen_port` field accepts 1024-65535 and adds `ListenPort` to the generated client `[Interface]`; omission or zero leaves port selection automatic. It has no global environment default and does not affect the server-side interface port. The optional `mtu` field accepts 1280-1420 and inherits `AWG_MTU` when omitted or zero. The optional `dns` field accepts one IPv4 address and inherits `AWG_DNS` when omitted or empty; DoH URLs and other formats are rejected. The optional `persistent_keepalive` field accepts 0-65535, inherits 25 when omitted, and is disabled by an explicit zero.

The complete default profile, including persisted generated H/S values and CPS environment values, is validated before the interface pool starts. Invalid defaults prevent startup and produce a field-specific log error. The accepted J/S/H/I constraints are the same as the [`awg_params` API contract](api.md#awg-params-object).

## Multi-Interface Behavior

When clients have different CPS parameters, the server creates separate AWG interfaces:

- Interface grouping key: H1-H4, S1-S4 only (client listen port, MTU, DNS, PersistentKeepalive, Jc/Jmin/Jmax, and I1-I5 do NOT create new interfaces)
- Each unique parameter set gets its own `awgN` interface (awg0, awg1, ...)
- Each interface listens on the explicit `port` from `awg_params`, or auto-assigned sequentially from `AWG_LISTEN_PORT`
- Interfaces are created on demand and destroyed when empty
- `AWG_MAX_INTERFACES` limits the total number of interfaces

Ensure your firewall allows the range of UDP ports that will be used.

## Persistence

Client data is stored in `{AWG_DATA_DIR}/clients.json`:

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
      "created_at": "2026-01-01T00:00:00Z",
      "awg_params": {
        "port": 51825,
        "client_listen_port": 54321,
        "mtu": 1280,
        "dns": "9.9.9.9",
        "persistent_keepalive": 60,
        "jc": 5,
        "jmin": 50,
        "jmax": 1000
      }
    }
  ]
}
```

Clients without custom parameters have `awg_params` omitted (uses automatic client listen-port selection and server defaults, including `AWG_DNS` and `PersistentKeepalive = 25`). Clients created before PSK support can also omit `preshared_key`. On startup, all clients are restored and interfaces are recreated as needed, using the persisted PSK when present.
