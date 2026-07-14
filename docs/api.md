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

With custom MTU, DNS, persistent keepalive, obfuscation parameters, and port:

```http
POST /api/clients
Content-Type: application/json

{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "awg_params": {
    "port": 51825,
    "mtu": 1280,
    "dns": "9.9.9.9",
    "persistent_keepalive": 60,
    "jc": 8,
    "jmin": 50,
    "jmax": 1000
  }
}
```

If `awg_params` is omitted, the client uses server defaults (global `AWG_MTU`, global `AWG_DNS`, `PersistentKeepalive = 25`, auto-generated H/S, and env Jc/Jmin/Jmax). Per-client params are merged over defaults. A custom `port` must be in the inclusive range 1024-65535; omitted or zero inherits automatic assignment. `dns` accepts one IPv4 address; omission or an empty string inherits `AWG_DNS`. DoH URLs, hostnames, CIDRs, IPv6 addresses, and lists are rejected. `persistent_keepalive` accepts 0-65535: omission inherits 25, while an explicit zero disables keepalive.

Every new client automatically receives a unique server-generated 32-byte preshared key. The API does not accept a PSK in the request and does not expose it in list, create, or update JSON responses. It is returned only as part of the authenticated client configuration.

**Response** `201 Created`:

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "address": "10.0.0.2",
  "created_at": "2026-01-01T00:00:00Z",
  "awg_params": {
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

- `400` — missing or invalid `id`, id too long (max 256 chars), `port` outside 1024-65535 (except zero for inheritance), `mtu` outside 1280-1420, invalid per-client `dns`, or `persistent_keepalive` outside 0-65535
- `409` — client with this id already exists, or requested port is already in use
- `503` — maximum number of interfaces reached

## Update Client

```http
PATCH /api/clients/{id}
Content-Type: application/json

{
  "awg_params": {
    "mtu": 1280,
    "dns": "9.9.9.9",
    "persistent_keepalive": 0,
    "jc": 10,
    "jmin": 100,
    "jmax": 2000
  }
}
```

Updates the client's MTU, DNS, persistent keepalive, and obfuscation parameters. When all other fields remain unchanged, changing only `mtu`, `dns`, or `persistent_keepalive` updates the generated client config without moving the peer to another interface. If interface-level parameters differ, the peer is moved to the appropriate interface (created on demand if needed).

`PATCH` replaces the complete `awg_params` override object rather than merging with the client's previous object. Include every custom field that must be retained. Set `awg_params` to `null` to revert all fields to server defaults. Changes to client-only values such as `mtu`, `dns`, and `persistent_keepalive` apply after the regenerated configuration is downloaded and reapplied on the client device.

**Response** `200 OK`:

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "address": "10.0.0.2",
  "created_at": "2026-01-01T00:00:00Z",
  "awg_params": {
    "mtu": 1280,
    "dns": "9.9.9.9",
    "persistent_keepalive": 0,
    "jc": 10,
    "jmin": 100,
    "jmax": 2000
  }
}
```

**Errors:**

- `400` — invalid request body, `port` outside 1024-65535 (except zero for inheritance), `mtu` outside 1280-1420, invalid per-client `dns`, or `persistent_keepalive` outside 0-65535
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

The MTU is the client's `awg_params.mtu` override, or the global `AWG_MTU` value when the override is omitted or zero. DNS is the client's validated IPv4 `awg_params.dns` override, or global `AWG_DNS` when omitted or empty. Persistent keepalive is the client's `awg_params.persistent_keepalive` override; omission uses 25 and zero disables it. `PresharedKey` is generated and stored by the server for new clients and must match the key installed on the server peer. Legacy clients created before PSK support omit this line and continue to work without a PSK. The Endpoint port matches the interface assigned to this client's obfuscation profile (explicit `port` from `awg_params`, or auto-assigned sequentially from base port).

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

All fields are optional. `mtu` accepts values from 1280 through 1420; omitted or zero inherits `AWG_MTU`. `dns` accepts a single IPv4 address; omitted or empty inherits `AWG_DNS`, and DoH URLs are not supported. `persistent_keepalive` accepts values from 0 through 65535; omission inherits 25 and zero explicitly disables it. Other parameters with value `0` (or empty string for I1-I5) are omitted, **except `s3`/`s4` which are always emitted (even when 0)**.

| Field | Type | Description |
| ----- | ---- | ----------- |
| `port` | int | UDP listen port for the interface, inclusive range 1024-65535. If omitted or zero, auto-assigned from the base port. Used in client config `Endpoint`. |
| `mtu` | int | MTU for this client's generated config, range 1280-1420. Omit or set to 0 to inherit `AWG_MTU`. Does not affect interface grouping. |
| `dns` | string | One IPv4 DNS server for the generated client `[Interface]`. Omit or set to an empty string to inherit `AWG_DNS`. DoH URLs, hostnames, CIDRs, IPv6 addresses, and lists are rejected. Does not affect interface grouping. |
| `persistent_keepalive` | int | Keepalive interval in seconds for the generated client `[Peer]`, range 0-65535. Omit to inherit 25; set to 0 to disable. Does not affect interface grouping. |
| `jc` | int | Junk packet count |
| `jmin` | int | Junk packet minimum size |
| `jmax` | int | Junk packet maximum size |
| `s1` - `s4` | int | Packet padding (init, response, underload, transport) |
| `h1` - `h4` | string | Packet header ranges, format `"min-max"` (init, response, underload, transport) |
| `i1` - `i5` | string | CPS signature packets (AmneziaWG 2.0) |

## Error Handling

- `401 Unauthorized` — missing or invalid `Authorization: Bearer` header
- `500 Internal Server Error` — returns generic `{"error": "internal server error"}` (details logged server-side only)
