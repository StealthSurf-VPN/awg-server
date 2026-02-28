# API Reference

Base URL: `http://<server_ip>:<AWG_HTTP_PORT>`

All endpoints require header: `Authorization: Bearer <AWG_API_TOKEN>`

## List Clients

```http
GET /api/clients
```

**Response** `200 OK`:

```json
[
  {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "name": "550e8400-e29b-41d4-a716-446655440000",
    "address": "10.0.0.2"
  }
]
```

Returns empty array `[]` if no clients.

## Create Client

```http
POST /api/clients
Content-Type: application/json

{"name": "550e8400-e29b-41d4-a716-446655440000"}
```

**Response** `201 Created`:

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "name": "550e8400-e29b-41d4-a716-446655440000",
  "address": "10.0.0.2"
}
```

**Errors:**

- `400` — missing or invalid `name`
- `409` — client with this name already exists

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
H1 = 1
H2 = 2
H3 = 3
H4 = 4

[Peer]
PublicKey = <base64>
Endpoint = 1.2.3.4:51820
AllowedIPs = 0.0.0.0/0, ::/0
PersistentKeepalive = 25
```

**Errors:**

- `404` — client not found

## Delete Client

```http
DELETE /api/clients/{id}
```

**Response** `204 No Content`

**Errors:**

- `404` — client not found

## Authentication Errors

- `401 Unauthorized` — missing or invalid `Authorization: Bearer` header
