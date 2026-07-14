# API Reference

Base URL: `http://<server_ip>:<AWG_HTTP_PORT>`

All endpoints require header: `Authorization: Bearer <AWG_API_TOKEN>` (except `/health`).

## Health Check

```http
GET /health
```

No authentication required.

**Response** `200 OK`:

```json
{"status": "ok"}
```

## List Clients

```http
GET /api/clients
```

**Response** `200 OK`:

```json
[
  {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "address": "10.0.0.2",
    "created_at": "2026-01-01T00:00:00Z",
    "awg_params": {
      "client_listen_port": 54321,
      "mtu": 1280,
      "dns": "9.9.9.9",
      "persistent_keepalive": 60,
      "jc": 5,
      "jmin": 50,
      "jmax": 1000
    },
    "routing": {
      "mode": "split",
      "allowed_ips": ["91.108.4.0/22", "149.154.160.0/20"]
    }
  }
]
```

Returns empty array `[]` if no clients. The `awg_params` field is omitted for clients using default server parameters. The `routing` field is always present and contains the effective routing policy; clients with omitted persisted routing are returned as `{"mode":"full"}`.

## Generate AWG Parameters

```http
POST /api/awg-params/generate
Authorization: Bearer <AWG_API_TOKEN>
```

This bearer-authenticated endpoint requires no request body. It generates a standalone H/S fragment with the server's secure-random algorithm and does not read, mutate, or persist any server or client state.

**Response** `200 OK` (`Content-Type: application/json`):

```json
{
  "h1": "234567-678901",
  "h2": "2345678-6789012",
  "h3": "23456789-67890123",
  "h4": "234567890-678901234",
  "s1": 42,
  "s2": 87
}
```

The response is the raw generated fragment, without a wrapper object, and its fields can be inserted into an `awg_params` object.

| Status | Meaning |
| ------ | ------- |
| `200` | A valid H1-H4 and S1-S2 fragment was generated. |
| `401` | The bearer token is missing or invalid. |
| `500` | Secure randomness failed. The response is the generic internal-error JSON and no state was changed. |

## Create Client

```http
POST /api/clients
Content-Type: application/json

{"id": "550e8400-e29b-41d4-a716-446655440000"}
```

With split routing:

```http
POST /api/clients
Content-Type: application/json

{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "routing": {
    "mode": "split",
    "allowed_ips": ["91.108.4.0/22", "149.154.160.0/20"]
  }
}
```

With custom server port, client listen port, MTU, a legacy DNS override, persistent keepalive, and obfuscation parameters:

```http
POST /api/clients
Content-Type: application/json

{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "awg_params": {
    "port": 51825,
    "client_listen_port": 54321,
    "mtu": 1280,
    "dns": "9.9.9.9",
    "persistent_keepalive": 60,
    "jc": 8,
    "jmin": 50,
    "jmax": 1000
  }
}
```

With mode-based custom DNS:

```http
POST /api/clients
Content-Type: application/json

{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "awg_params": {
    "dns_mode": "custom",
    "dns_servers": ["1.1.1.1", "1.0.0.1"]
  }
}
```

If `awg_params` is omitted, the client uses server defaults (global `AWG_MTU`, global `AWG_DNS`, `PersistentKeepalive = 25`, auto-generated H/S, and env Jc/Jmin/Jmax). Per-client params are merged over defaults. A custom server-side `port` must be in the inclusive range 1024-65535; omitted or zero uses automatic server interface assignment. `client_listen_port` accepts the same range and adds `ListenPort` to the generated client `[Interface]`; omitted or zero leaves client-side port selection automatic. DNS supports the backward-compatible `dns` field and the mode-based `dns_mode`/`dns_servers` fields described in [DNS Settings](#dns-settings). `persistent_keepalive` accepts 0-65535: omission inherits 25, while an explicit zero disables keepalive.

If `routing` is omitted, `null`, or `{"mode":"full"}`, the client uses full-tunnel routing. See [Routing Object](#routing-object) for split-tunnel behavior and validation.

Every new client automatically receives a unique server-generated 32-byte preshared key. The API does not accept a PSK in the request and does not expose it in list, create, or update JSON responses. It is returned only as part of the authenticated client configuration.

**Response** `201 Created`:

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "address": "10.0.0.2",
  "created_at": "2026-01-01T00:00:00Z",
  "awg_params": {
    "client_listen_port": 54321,
    "mtu": 1280,
    "dns": "9.9.9.9",
    "persistent_keepalive": 60,
    "jc": 5,
    "jmin": 50,
    "jmax": 1000
  },
  "routing": {
    "mode": "full"
  }
}
```

**Errors:**

- `400` — missing or invalid `id`, id too long (max 256 chars), invalid `awg_params`, or invalid `routing`
- `409` — client with this id already exists, or requested port is already in use
- `503` — maximum number of interfaces reached

## Update Client

```http
PATCH /api/clients/{id}
Content-Type: application/json

{
  "awg_params": {
    "client_listen_port": 54321,
    "mtu": 1280,
    "dns_mode": "custom",
    "dns_servers": ["9.9.9.9", "149.112.112.112"],
    "persistent_keepalive": 0,
    "jc": 10,
    "jmin": 100,
    "jmax": 1000
  },
  "routing": {
    "mode": "split",
    "allowed_ips": ["91.108.4.0/22", "149.154.160.0/20"]
  }
}
```

Updates `awg_params` and `routing` independently. Omitting either field preserves its current value, JSON `null` resets that field to its default behavior, and an object replaces the complete stored value for that field. A request containing neither field returns `400 Bad Request`.

For `awg_params`, include every custom field that must be retained; `null` reverts all fields to their automatic or server-default behavior. The object is a complete replacement, not a field merge. For example, preserving custom DNS requires sending both `"dns_mode":"custom"` and the complete `dns_servers` list in the same object, while switching to system DNS can replace them with `"dns_mode":"system"`. When all other fields remain unchanged, changing only `client_listen_port`, `mtu`, a valid DNS setting, or `persistent_keepalive` updates the generated client config without moving the peer to another interface. If interface-level parameters differ, the peer is moved to the appropriate interface (created on demand if needed).

For `routing`, an object replaces the complete policy and `null` resets it to full tunnel. Routing-only updates never move the peer or change its server-side `/32`; download and reapply the regenerated configuration on the client device for the new routing policy to take effect. The same re-download/reapply requirement applies to other client-only values.

**Response** `200 OK`:

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "address": "10.0.0.2",
  "created_at": "2026-01-01T00:00:00Z",
  "awg_params": {
    "client_listen_port": 54321,
    "mtu": 1280,
    "dns_mode": "custom",
    "dns_servers": ["9.9.9.9", "149.112.112.112"],
    "persistent_keepalive": 0,
    "jc": 10,
    "jmin": 100,
    "jmax": 1000
  },
  "routing": {
    "mode": "split",
    "allowed_ips": ["91.108.4.0/22", "149.154.160.0/20"]
  }
}
```

**Errors:**

- `400` — invalid request body, neither supported field supplied, invalid `awg_params`, or invalid `routing`
- `404` — client not found
- `409` — requested port is already in use, or port change on shared interface
- `503` — maximum number of interfaces reached

## Regenerate Client AWG Parameters

```http
POST /api/clients/{id}/regenerate-awg-params
Authorization: Bearer <AWG_API_TOKEN>
```

This bearer-authenticated endpoint requires no request body. It generates a new H1-H4 and S1-S2 set for the client, validates the resulting effective profile, migrates the peer through the existing interface pool, and returns the normal client response shape.

**Response** `200 OK` (`Content-Type: application/json`):

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "address": "10.0.0.2",
  "created_at": "2026-01-01T00:00:00Z",
  "awg_params": {
    "port": 51825,
    "client_listen_port": 54321,
    "mtu": 1280,
    "dns_mode": "custom",
    "dns_servers": ["9.9.9.9", "149.112.112.112"],
    "persistent_keepalive": 60,
    "jc": 8,
    "jmin": 50,
    "jmax": 1000,
    "s1": 42,
    "s2": 87,
    "h1": "234567-678901",
    "h2": "2345678-6789012",
    "h3": "23456789-67890123",
    "h4": "234567890-678901234"
  },
  "routing": {
    "mode": "full"
  }
}
```

Only H1-H4 and S1-S2 are replaced. The operation preserves the server port, client listen port, MTU, all legacy and mode-based DNS fields, persistent keepalive, Jc/Jmin/Jmax, S3/S4, I1-I5, routing, client ID, address, creation time, private/public keys, and preshared key. If the client previously inherited all AWG overrides, the stored object gains only the generated H/S fields and every unrelated value continues to inherit its server default.

> **Warning:** A successful response means the server-side peer has already moved to the new H/S profile, so the old client configuration is immediately invalid. Immediately fetch `GET /api/clients/{id}/configuration`, then deliver and reapply that configuration on the client device.

| Status | Meaning |
| ------ | ------- |
| `200` | The client was migrated and the updated normal client response is returned. |
| `400` | The resulting effective AWG profile is invalid. |
| `401` | The bearer token is missing or invalid. |
| `404` | The client was not found. |
| `409` | The preserved server port conflicts with another interface, including the shared explicit-port case described below. |
| `503` | The interface limit prevents migration. |
| `500` | Secure randomness, distinct-parameter generation, or another internal device operation failed. The response is generic. |

The explicit shared-port conflict is a `409`: when a client has a fixed server-side `port` and shares its current interface, the new H/S grouping key needs a different interface while that same port is still occupied by the shared old interface. The failed request leaves the stored client parameters unchanged.

## Get Client Configuration

```http
GET /api/clients/{id}/configuration
```

**Response** `200 OK` (`Content-Type: text/plain`):

```ini
[Interface]
PrivateKey = <base64>
ListenPort = 54321
Address = 10.0.0.2/32
DNS = 9.9.9.9, 149.112.112.112
MTU = 1280
Jc = 5
Jmin = 50
Jmax = 1000
S1 = 42
S2 = 87
H1 = 234567-678901
H2 = 2345678-6789012
H3 = 23456789-67890123
H4 = 234567890-678901234

[Peer]
PublicKey = <base64>
PresharedKey = <base64>
Endpoint = 1.2.3.4:51820
AllowedIPs = 0.0.0.0/0, ::/0
PersistentKeepalive = 60
```

`ListenPort` is included only when `awg_params.client_listen_port` is between 1024 and 65535; omission or zero lets the client choose automatically. It is local to the client and does not change the server `Endpoint` port. The MTU is the client's `awg_params.mtu` override, or the global `AWG_MTU` value when the override is omitted or zero. DNS uses global `AWG_DNS` for inherited/default mode, one address for the legacy override, or the normalized comma-separated `dns_servers` list for custom mode. In system mode the `DNS` line is omitted completely so the client keeps its system resolver. Persistent keepalive is the client's `awg_params.persistent_keepalive` override; omission uses 25 and zero disables it. `PresharedKey` is generated and stored by the server for new clients and must match the key installed on the server peer. Legacy clients created before PSK support omit this line and continue to work without a PSK. The Endpoint port matches the interface assigned to this client's obfuscation profile (explicit `port` from `awg_params`, or auto-assigned sequentially from base port).

**Errors:**

- `404` — client not found

## Get Client Stats

```http
GET /api/clients/{id}/stats
```

**Response** `200 OK`:

```json
{
  "rx_bytes": 1073741824,
  "tx_bytes": 5368709120,
  "last_handshake": "2026-04-01T12:00:00Z"
}
```

Returns accumulated traffic counters (survive reboots) and last handshake time. Returns zeros if the client has never connected. `last_handshake` is omitted if no handshake occurred.

**Errors:**

- `404` — client not found

## Delete Client

```http
DELETE /api/clients/{id}
```

**Response** `204 No Content`

If this was the last client on an interface, the interface is automatically destroyed.

**Errors:**

- `404` — client not found

## Routing Object

`routing` is a top-level client field, separate from `awg_params`. It controls the `AllowedIPs` line rendered in the generated client configuration.

| Mode | `allowed_ips` | Generated behavior |
| ---- | ------------- | ------------------ |
| `full` | Omitted or empty | `AllowedIPs = 0.0.0.0/0, ::/0` |
| `split` | One or more IPv4 CIDRs | Only the normalized listed prefixes are rendered |

For `split`, each CIDR is masked to its network prefix. Duplicate normalized prefixes are removed while preserving the first occurrence order; for example, `10.1.2.3/8`, `10.0.0.0/8`, `192.168.1.7/24` becomes `10.0.0.0/8`, `192.168.1.0/24`.

Missing or unknown modes, a non-empty `allowed_ips` array with `full`, an empty `allowed_ips` array with `split`, malformed CIDRs, and IPv6 CIDRs in `split` return `400 Bad Request` before client state changes. Syntactically valid private, overlapping, and default IPv4 CIDRs such as `0.0.0.0/0` are intentionally accepted; authenticated callers are responsible for the resulting route selection.

Omitted routing and explicit `full` are equivalent. API responses always expose the effective object as `{"mode":"full"}` or a normalized `split` object. Routing changes affect only the generated client configuration: the client must download and reapply it, while interface grouping, peer migration, and the server-side peer `/32` remain unchanged. Domain, application, and geosite routing require separate client-side logic and are not accepted by this CIDR-only contract.

## AWG Params Object

All fields are optional. Unless documented otherwise below, zero integer values and empty strings inherit the corresponding server default. Validation is performed both on the supplied override and on the effective profile after inheritance.

| Field | Type | Description |
| ----- | ---- | ----------- |
| `port` | int | UDP listen port for the interface, inclusive range 1024-65535. If omitted or zero, auto-assigned from the base port. Used in client config `Endpoint`. |
| `client_listen_port` | int | Local UDP listen port for the generated client `[Interface]`, inclusive range 1024-65535. If omitted or zero, `ListenPort` is omitted and the client selects a port automatically. Does not affect server interface grouping or `Endpoint`. |
| `mtu` | int | MTU for this client's generated config, range 1280-1420. Omit or set to 0 to inherit `AWG_MTU`. Does not affect interface grouping. |
| `dns` | string | Legacy-compatible single IPv4 DNS override. Omit or set to an empty string to inherit `AWG_DNS`. Cannot be combined with `dns_mode` or `dns_servers`, even when explicitly empty. Does not affect interface grouping. |
| `dns_mode` | string | DNS behavior: `default`, `custom`, or `system`. Cannot be combined with legacy `dns`. Does not affect interface grouping. |
| `dns_servers` | string[] | DNS servers for `custom` mode. Every value must be an IPv4 address. Requires `dns_mode` and cannot be combined with legacy `dns`. Does not affect interface grouping. |
| `persistent_keepalive` | int | Keepalive interval in seconds for the generated client `[Peer]`, range 0-65535. Omit to inherit 25; set to 0 to disable. Does not affect interface grouping. |
| `jc` | int | Junk packet count, range 0-128. Zero inherits the server default. |
| `jmin` | int | Junk packet minimum size, range 0-1280. Zero inherits the server default. When effective `jc > 0`, effective `jmin` must be positive and less than `jmax`. |
| `jmax` | int | Junk packet maximum size, range 0-1280. Zero inherits the server default. When effective `jc > 0`, effective `jmax` must be positive and greater than `jmin`. |
| `s1` | int | Init packet padding, range 0-1132. Zero inherits the server default. |
| `s2` | int | Response packet padding, range 0-1188. Zero inherits the server default; effective `s2` must not equal `s1 + 56`. |
| `s3` | int | Underload packet padding, range 0-64. Zero inherits the server default. |
| `s4` | int | Transport packet padding, range 0-32. Zero inherits the server default. |
| `h1` - `h4` | string | Unsigned decimal `uint32` values or inclusive `start-end` ranges. Empty values inherit server defaults; all four effective ranges are required and must not overlap. |
| `i1` - `i5` | string | Tag-only CPS strings. Supported tags are `<b 0xHEX>`, `<t>`, `<r N>`, `<rc N>`, and `<rd N>`; `N` is 0-1000 and each expanded packet is at most 1280 bytes. Empty values inherit server defaults. |

### DNS Settings

The legacy `dns` field remains supported, while new clients can select an explicit mode. The accepted shapes and generated behavior are:

| Form | Accepted fields | Generated `[Interface]` behavior |
| ---- | --------------- | -------------------------------- |
| Inherited legacy default | All DNS fields omitted, or `"dns":""` supplied alone | `DNS = <AWG_DNS>` |
| Legacy custom | `"dns":"9.9.9.9"` supplied alone | `DNS = 9.9.9.9` |
| `default` mode | `"dns_mode":"default"`; `dns_servers` omitted or empty | `DNS = <AWG_DNS>` |
| `custom` mode | `"dns_mode":"custom"` and one or more `dns_servers` | One comma-and-space-separated line, for example `DNS = 1.1.1.1, 1.0.0.1` |
| `system` mode | `"dns_mode":"system"`; `dns_servers` omitted or empty | No `DNS` line is generated |

Field presence is validated strictly. Supplying legacy `dns` forbids both `dns_mode` and `dns_servers`, including combinations such as `"dns":""` with a mode or list. Supplying `dns_servers` requires `dns_mode` even when the list is explicitly empty. An explicitly empty `dns_mode` is invalid. JSON `null` also counts as an explicitly supplied nested DNS field and follows the same presence rules. `custom` requires a non-empty list; `default` and `system` reject non-empty lists.

Every non-empty legacy `dns` value and every `dns_servers` item must be a plain IPv4 address. DoH URLs, hostnames, CIDRs, IPv6 addresses, and mixed-format values are rejected with `400 Bad Request`. Custom server addresses are canonicalized and duplicates are removed stably before persistence, preserving the first occurrence order.

Malformed ranges, overlapping effective header ranges, unsupported CPS tags, text outside CPS tags, control characters, and expanded CPS packets larger than 1280 bytes are rejected with `400 Bad Request` before client state is changed.

## Error Handling

- `401 Unauthorized` — missing or invalid `Authorization: Bearer` header
- `500 Internal Server Error` — returns generic `{"error": "internal server error"}` (details logged server-side only)
