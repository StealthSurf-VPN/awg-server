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
    }
  }
]
```

Returns empty array `[]` if no clients. The `awg_params` field is omitted for clients using default server parameters.

## Create Client

```http
POST /api/clients
Content-Type: application/json

{"id": "550e8400-e29b-41d4-a716-446655440000"}
```

With custom server port, client listen port, MTU, DNS, persistent keepalive, and obfuscation parameters:

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

If `awg_params` is omitted, the client uses server defaults (global `AWG_MTU`, global `AWG_DNS`, `PersistentKeepalive = 25`, auto-generated H/S, and env Jc/Jmin/Jmax). Per-client params are merged over defaults. A custom server-side `port` must be in the inclusive range 1024-65535; omitted or zero uses automatic server interface assignment. `client_listen_port` accepts the same range and adds `ListenPort` to the generated client `[Interface]`; omitted or zero leaves client-side port selection automatic. `dns` accepts one IPv4 address; omission or an empty string inherits `AWG_DNS`. DoH URLs, hostnames, CIDRs, IPv6 addresses, and lists are rejected. `persistent_keepalive` accepts 0-65535: omission inherits 25, while an explicit zero disables keepalive.

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
  }
}
```

**Errors:**

- `400` — missing or invalid `id`, id too long (max 256 chars), or invalid `awg_params`
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
    "dns": "9.9.9.9",
    "persistent_keepalive": 0,
    "jc": 10,
    "jmin": 100,
    "jmax": 1000
  }
}
```

Updates the client's local listen port, MTU, DNS, persistent keepalive, and obfuscation parameters. When all other fields remain unchanged, changing only `client_listen_port`, `mtu`, `dns`, or `persistent_keepalive` updates the generated client config without moving the peer to another interface. If interface-level parameters differ, the peer is moved to the appropriate interface (created on demand if needed).

`PATCH` replaces the complete `awg_params` override object rather than merging with the client's previous object. Include every custom field that must be retained. Set `awg_params` to `null` to revert all fields to their automatic or server-default behavior. Changes to client-only values such as `client_listen_port`, `mtu`, `dns`, and `persistent_keepalive` apply after the regenerated configuration is downloaded and reapplied on the client device.

**Response** `200 OK`:

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "address": "10.0.0.2",
  "created_at": "2026-01-01T00:00:00Z",
  "awg_params": {
    "client_listen_port": 54321,
    "mtu": 1280,
    "dns": "9.9.9.9",
    "persistent_keepalive": 0,
    "jc": 10,
    "jmin": 100,
    "jmax": 1000
  }
}
```

**Errors:**

- `400` — invalid request body or invalid `awg_params`
- `404` — client not found
- `409` — requested port is already in use, or port change on shared interface
- `503` — maximum number of interfaces reached

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
DNS = 9.9.9.9
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

`ListenPort` is included only when `awg_params.client_listen_port` is between 1024 and 65535; omission or zero lets the client choose automatically. It is local to the client and does not change the server `Endpoint` port. The MTU is the client's `awg_params.mtu` override, or the global `AWG_MTU` value when the override is omitted or zero. DNS is the client's validated IPv4 `awg_params.dns` override, or global `AWG_DNS` when omitted or empty. Persistent keepalive is the client's `awg_params.persistent_keepalive` override; omission uses 25 and zero disables it. `PresharedKey` is generated and stored by the server for new clients and must match the key installed on the server peer. Legacy clients created before PSK support omit this line and continue to work without a PSK. The Endpoint port matches the interface assigned to this client's obfuscation profile (explicit `port` from `awg_params`, or auto-assigned sequentially from base port).

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

## AWG Params Object

All fields are optional. Unless documented otherwise below, zero integer values and empty strings inherit the corresponding server default. Validation is performed both on the supplied override and on the effective profile after inheritance.

| Field | Type | Description |
| ----- | ---- | ----------- |
| `port` | int | UDP listen port for the interface, inclusive range 1024-65535. If omitted or zero, auto-assigned from the base port. Used in client config `Endpoint`. |
| `client_listen_port` | int | Local UDP listen port for the generated client `[Interface]`, inclusive range 1024-65535. If omitted or zero, `ListenPort` is omitted and the client selects a port automatically. Does not affect server interface grouping or `Endpoint`. |
| `mtu` | int | MTU for this client's generated config, range 1280-1420. Omit or set to 0 to inherit `AWG_MTU`. Does not affect interface grouping. |
| `dns` | string | One IPv4 DNS server for the generated client `[Interface]`. Omit or set to an empty string to inherit `AWG_DNS`. DoH URLs, hostnames, CIDRs, IPv6 addresses, and lists are rejected. Does not affect interface grouping. |
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

Malformed ranges, overlapping effective header ranges, unsupported CPS tags, text outside CPS tags, control characters, and expanded CPS packets larger than 1280 bytes are rejected with `400 Bad Request` before client state is changed.

## Error Handling

- `401 Unauthorized` — missing or invalid `Authorization: Bearer` header
- `500 Internal Server Error` — returns generic `{"error": "internal server error"}` (details logged server-side only)
