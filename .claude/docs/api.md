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

With custom obfuscation parameters and port:

```http
POST /api/clients
Content-Type: application/json

{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "awg_params": {
    "port": 51825,
    "jc": 5,
    "jmin": 50,
    "jmax": 1000,
    "s1": 15,
    "s2": 15,
    "h1": 12345,
    "h2": 23456,
    "h3": 34567,
    "h4": 45678
  }
}
```

If `awg_params` is omitted, the client uses server defaults (auto-generated H/S + env Jc/Jmin/Jmax). Per-client params are merged over defaults — non-zero values override, zero/empty values inherit defaults.

**Response** `201 Created`:

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "address": "10.0.0.2",
  "created_at": "2026-01-01T00:00:00Z",
  "awg_params": {
    "jc": 5,
    "jmin": 50,
    "jmax": 1000
  }
}
```

**Errors:**

- `400` — missing or invalid `id`, or id too long (max 256 chars)
- `409` — client with this id already exists, or requested port is already in use
- `503` — maximum number of interfaces reached

## Update Client

```http
PATCH /api/clients/{id}
Content-Type: application/json

{
  "awg_params": {
    "jc": 10,
    "jmin": 100,
    "jmax": 2000
  }
}
```

Updates the client's obfuscation parameters. If the new parameters differ from the current ones, the client's peer is moved to the appropriate interface (created on demand if needed). Set `awg_params` to `null` to revert to server defaults.

**Response** `200 OK`:

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "address": "10.0.0.2",
  "created_at": "2026-01-01T00:00:00Z",
  "awg_params": {
    "jc": 10,
    "jmin": 100,
    "jmax": 2000
  }
}
```

**Errors:**

- `400` — invalid request body
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
DNS = 1.1.1.1
MTU = 1420
Jc = 5
Jmin = 50
Jmax = 1000
H1 = 12345
H2 = 23456
H3 = 34567
H4 = 45678

[Peer]
PublicKey = <base64>
Endpoint = 1.2.3.4:51820
AllowedIPs = 0.0.0.0/0, ::/0
PersistentKeepalive = 25
```

The Endpoint port matches the interface assigned to this client's obfuscation profile (explicit `port` from `awg_params`, or auto-assigned sequentially from base port).

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

All fields are optional. Parameters with value `0` (or empty string for I1-I5) are omitted.

| Field | Type | Description |
| ----- | ---- | ----------- |
| `port` | int | UDP listen port for the interface. If omitted, auto-assigned (base port + index). Used in client config `Endpoint`. |
| `jc` | int | Junk packet count |
| `jmin` | int | Junk packet minimum size |
| `jmax` | int | Junk packet maximum size |
| `s1` - `s4` | int | Packet padding (init, response, underload, transport) |
| `h1` - `h4` | uint32 | Packet headers (init, response, underload, transport) |
| `i1` - `i5` | string | CPS signature packets (AmneziaWG 2.0) |

## Error Handling

- `401 Unauthorized` — missing or invalid `Authorization: Bearer` header
- `500 Internal Server Error` — returns generic `{"error": "internal server error"}` (details logged server-side only)
