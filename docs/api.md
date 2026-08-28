# API Reference

Base URL: `http://<server_ip>:<AWG_HTTP_PORT>`

All endpoints require header: `Authorization: Bearer <AWG_API_TOKEN>` (except `/health`).

## HTTP Routing, Authentication, and Bodies

The server uses the Go method-aware `net/http` `ServeMux`. These are the complete registered routes:

| Method | Path | Authentication | Success | Handler errors |
| ------ | ---- | -------------- | ------- | -------------- |
| `GET` | `/health` | None | `200` | None |
| `GET` | `/api/capabilities` | Bearer | `200` | `401` |
| `GET` | `/api/clients` | Bearer | `200` | `401` |
| `POST` | `/api/clients` | Bearer | `201` | `400`, `401`, `409`, `503`, `500` |
| `PATCH` | `/api/clients/lan-group` | Bearer | `200` | `400`, `401`, `404`, `500` |
| `PATCH` | `/api/clients/{id}` | Bearer | `200` | `400`, `401`, `404`, `409`, `503`, `500` |
| `GET` | `/api/clients/{id}/configuration` | Bearer | `200` | `401`, `404`, `500` |
| `GET` | `/api/clients/{id}/stats` | Bearer | `200` | `401`, `404` |
| `DELETE` | `/api/clients/{id}` | Bearer | `204` | `401`, `404`, `500` |
| `POST` | `/api/awg-params/generate` | Bearer | `200` | `400`, `401`, `500` |
| `POST` | `/api/clients/{id}/regenerate-awg-params` | Bearer | `200` | `400`, `401`, `404`, `409`, `503`, `500` |

There is no `GET /api/clients/{id}` endpoint. A method that does not match an otherwise known path receives the mux's standard `405 Method Not Allowed`; an unknown path receives its standard `404 Not Found`. Those mux-generated responses are plain text and do not use the API JSON error envelope. A registered `GET` pattern also serves `HEAD`, with the same authentication requirement. No CORS or custom `OPTIONS` route is registered.

The bearer check runs before a matched protected handler. A missing `Authorization` header returns `401` with `{"error":"missing authorization header"}`; a non-Bearer scheme or wrong token returns `401` with `{"error":"invalid token"}`.

`POST /api/clients`, `PATCH /api/clients/lan-group`, and `PATCH /api/clients/{id}` read at most 1 MiB and require exactly one JSON value. Empty, malformed, oversized, trailing-garbage, and multiple-value bodies return `400` before manager mutation. Unknown JSON fields are ignored; consequently, a PATCH containing no recognized top-level field still returns the empty-update `400`. The server does not require a request `Content-Type`, although clients should send `application/json`. All other handlers ignore the request body, including the two POST action endpoints.

## Health Check

```http
GET /health
```

No authentication required.

`HEAD /health` is also served by the GET pattern. Other methods receive the mux's standard `405` response.

**Response** `200 OK`:

```json
{"status": "ok"}
```

## Capabilities

```http
GET /api/capabilities
Authorization: Bearer <AWG_API_TOKEN>
```

**Response** `200 OK`:

```json
{"awg_protocol_3_1":true,"lan_group_isolation":true}
```

`lan_group_isolation: true` guarantees persisted `lan_group_id`, create and
atomic batch APIs, fail-closed firewall isolation, and an explicit VPN network
in every generated `AllowedIPs`. `awg_protocol_3_1: true` is not optimistic:
normal startup first validates persisted state and passes the AWG 3.1 runtime
qualifier before it creates a pool, firewall state, or HTTP server. A backend
must treat a missing endpoint or a `false` value as lack of that feature.

## Protocol versions and private state

`protocol_version` has canonical values `"2.0"` and `"3.1"`. API input also
accepts the legacy alias `"2"`, which normalizes to `"2.0"`. `null`,
non-string input, and other aliases return `400` before manager mutation.

- A missing version on disk is legacy 2.0 only; it does not inherit the new
  default after restart.
- An omitted version in `POST /api/clients` uses
  `AWG_DEFAULT_PROTOCOL_VERSION` (default `3.1`).
- Every saved record and every public client response has a canonical version.
- On `PATCH /api/clients/{id}`, omission preserves the existing version;
  a supported explicit change is a transactional interface migration.

The persisted field is stricter than API input: `clients.json` accepts only the
exact lower-case `protocol_version` name and canonical `"2.0"` or `"3.1"`
string values. A missing persisted field is normalized as legacy 2.0; a
persisted alias `"2"`, `null`, number, unknown value, or case-variant field
fails restore rather than being silently reinterpreted.

Client list/create/update/regenerate/LAN-group responses never contain a
private key, peer PSK, `header_key_id`, or a header-protection key. The private
header-protection key occurs only in the authenticated generated configuration
for a 3.1 client. It is not a synthetic `ProtocolVersion` configuration line.

## List Clients

```http
GET /api/clients
```

**Response** `200 OK`:

```json
[
  {
    "id": "550e8400-e29b-41d4-a716-446655440000",
    "protocol_version": "2.0",
    "address": "10.0.0.2",
    "lan_group_id": "peer:550e8400-e29b-41d4-a716-446655440000",
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

Returns empty array `[]` if no clients. The `awg_params` field is omitted for
clients using default server parameters. The `routing` field is always present
and contains the effective routing policy; clients with omitted persisted
routing are returned as `{"mode":"full"}`. The response reports stored
overrides rather than every inherited default, and always reports the canonical
`protocol_version`.

## Generate AWG Parameters

```http
POST /api/awg-params/generate?protocol_version=3.1
Authorization: Bearer <AWG_API_TOKEN>
```

This bearer-authenticated endpoint is defined without a request body. The
optional query must be the single `protocol_version` key and follows the same
`2`/`2.0`/`3.1` rules as client requests. When omitted, it uses
`AWG_DEFAULT_PROTOCOL_VERSION`. An unknown key, duplicate parameter, or invalid
value returns `400`. Supplied body bytes are ignored.

It generates a standalone public H/S fragment with the server's secure-random
algorithm and does not read, mutate, or persist server or client state. It
never returns a header-protection key.

**Response** `200 OK` (`Content-Type: application/json`):

```json
{
  "h1": "123456",
  "h2": "1234567",
  "h3": "12345678",
  "h4": "123456789",
  "s1": 15,
  "s2": 16,
  "s3": 15,
  "s4": 12
}
```

The response is the raw generated fragment, without a wrapper object, and its
fields can be inserted into an `awg_params` object. The example is the 3.1
shape: it has fixed unique H values plus S1-S4. For 2.0, the result retains the
legacy non-overlapping H ranges and S1/S2 only.

Errors use the common [JSON error envelope](#error-handling).

| Status | Meaning |
| ------ | ------- |
| `200` | A valid fragment for the selected target version was generated. |
| `400` | The `protocol_version` query is malformed, repeated, unknown, or invalid. |
| `401` | The bearer token is missing or invalid. |
| `500` | Secure randomness failed. The response is the generic internal-error JSON and no state was changed. |

## Create Client

```http
POST /api/clients
Content-Type: application/json

{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "lan_group_id": "peer:primary-connection-id"
}
```

Because `protocol_version` is omitted, this uses the configured default (3.1
with the shipped default). Send `"protocol_version":"2"` or `"2.0"` to
create a legacy client explicitly, or `"protocol_version":"3.1"` to make
that choice explicit. API input accepts no other protocol value.

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

With mode-based custom DNS and split routing exclusions:

```http
POST /api/clients
Content-Type: application/json

{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "awg_params": {
    "dns_mode": "custom",
    "dns_servers": ["1.1.1.1", "1.0.0.1"]
  },
  "routing": {
    "mode": "split",
    "allowed_ips": ["10.0.0.0/8"],
    "excluded_ips": ["10.20.0.0/16"]
  }
}
```

If `awg_params` is omitted, the client receives defaults for its target
protocol. 2.0 uses the legacy `AWG_MTU`, generated ranged H1-H4/S1-S2, and
legacy CPS defaults. 3.1 uses `AWG31_MTU`, persisted fixed H1-H4/S1-S4, and
the strict AWG31 range/toggle defaults. Per-client params are merged over the
selected defaults after version-aware validation. A custom server-side `port`
must be in the inclusive range 1024-65535; omitted or zero uses automatic
interface allocation. On create, a new peer can join an existing profile with
port 0 or that interface's actual port. `client_listen_port` accepts the same
range and adds `ListenPort` to the generated client `[Interface]`; omitted or
zero leaves client-side port selection automatic. DNS supports the
backward-compatible `dns` field and the mode-based `dns_mode`/`dns_servers`
fields described in [DNS Settings](#dns-settings).

If `routing` is omitted, `null`, or `{"mode":"full"}`, the client uses full-tunnel routing. See [Routing Object](#routing-object) for split-tunnel behavior and validation.

`lan_group_id` is an opaque non-empty membership key. The StealthSurf backend uses `peer:<primary-connection-id>`. If it is omitted or empty on create, the server stores `peer:<id>`, keeping the new client isolated until an explicit batch update groups it with another client. Legacy persisted clients without this field receive and persist the same unique default during startup.

Every new client automatically receives a unique server-generated 32-byte preshared key. The API does not accept a PSK in the request and does not expose it in list, create, or update JSON responses. It is returned only as part of the authenticated client configuration.

The request body is limited to 1 MiB and must contain exactly one JSON value. A larger body, malformed JSON, trailing garbage, or a second JSON value is rejected as an invalid request with `400 Bad Request` before client state changes. Unknown fields are ignored. The `id` must be non-empty and no longer than 256 Unicode characters.

Creation installs a DROP-only `AWG-LAN` chain before staging the interface, peer, and route. It then saves a prospective `clients.json`, commits the client to memory, and rebuilds same-group allow rules. A device or persistence failure returns the generic `500`; after a save failure the server attempts to remove the staged peer and any now-empty interface, while LAN traffic remains blocked. If the final firewall rebuild fails, the created client is already committed and persisted, the API returns generic `500`, and all inter-client traffic stays blocked until a successful rebuild or restart.

**Response** `201 Created`:

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "protocol_version": "3.1",
  "address": "10.0.0.2",
  "lan_group_id": "peer:primary-connection-id",
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

- `400` — malformed request body, missing or invalid `id`, invalid/null/non-string `protocol_version`, id too long (max 256 Unicode characters), invalid `awg_params`, or invalid `routing`
- `401` — bearer token missing or invalid
- `409` — client with this id already exists, requested port is already in use, or an existing profile uses a different actual port
- `503` — maximum number of interfaces reached
- `500` — key generation, IP allocation, device/network setup, `clients.json` persistence, or firewall rebuild failed; the response is generic and LAN traffic remains fail closed

## Update LAN Group

```http
PATCH /api/clients/lan-group
Content-Type: application/json

{
  "client_ids": ["primary-id", "device-id"],
  "lan_group_id": "peer:primary-id"
}
```

The request must contain a non-empty list of unique existing client IDs and a non-empty `lan_group_id`. The manager validates every ID under its existing write mutex before any firewall or persisted state mutation. If any ID is missing, no client is changed and the firewall is not touched.

After validation, the server atomically replaces `AWG-LAN` with a DROP-only chain, updates every requested record in one prospective `clients.json` replacement, commits every in-memory record, and rebuilds same-group allows. No error path restores permissive rules. A persistence failure leaves the previous membership authoritative and LAN traffic blocked. A final firewall failure leaves the complete new membership authoritative and persisted, returns generic `500`, and keeps all inter-client traffic blocked until a successful rebuild or restart.

**Response** `200 OK`:

```json
{
  "clients": [
    {
      "id": "primary-id",
      "protocol_version": "3.1",
      "address": "10.100.0.2",
      "lan_group_id": "peer:primary-id",
      "created_at": "2026-07-22T00:00:00Z",
      "routing": {"mode": "full"}
    },
    {
      "id": "device-id",
      "protocol_version": "2.0",
      "address": "10.100.0.3",
      "lan_group_id": "peer:primary-id",
      "created_at": "2026-07-22T00:00:01Z",
      "routing": {"mode": "full"}
    }
  ]
}
```

Each entry uses the same safe public shape as `GET /api/clients`; private,
public, preshared, and header-protection keys/references are never included.
Entries follow request order and include a canonical `protocol_version`.

**Errors:**

- `400` — malformed body, empty or duplicate `client_ids`, or empty `lan_group_id`
- `401` — bearer token missing or invalid
- `404` — at least one client does not exist; no mutation occurred
- `500` — persistence or firewall operation failed; the response is generic and LAN traffic remains fail closed

## Update Client

```http
PATCH /api/clients/{id}
Content-Type: application/json

{
  "awg_params": {
    "dns_mode": "custom",
    "dns_servers": ["1.1.1.1", "1.0.0.1"]
  },
  "routing": {
    "mode": "split",
    "allowed_ips": ["10.0.0.0/8"],
    "excluded_ips": ["10.20.0.0/16"]
  }
}
```

Updates `protocol_version`, `awg_params`, and `routing` independently. Omitting
any field preserves it. For `awg_params` and `routing`, JSON `null` resets the
field to the target version's default behavior; an object replaces the complete
stored object. `protocol_version` must be a non-null string: `2`, `2.0`, and
`3.1` are accepted, while `null` and other values are `400`. A version-only
PATCH is valid and can migrate a peer. A request containing none of the three
recognized fields returns `400 Bad Request`.

An explicit version change resolves the target version before parameters are
validated. It takes the required complete usage snapshot, migrates the peer if
the effective profile changes, saves prospective state, and only then commits
memory. A 2.0-to-3.1 migration assigns the default private 3.1 key reference.
A 3.1-to-2.0 migration stages removal of now-unreferenced non-default 3.1 key
state in prospective data; it becomes authoritative only after the new state is
saved. Common incompatible overrides are rejected rather than silently
dropped; use `"awg_params":null` or a corrected complete object with the
target version.

For `awg_params`, include every custom field that must be retained; `null` reverts all fields to their automatic or server-default behavior. An empty object is normalized to the same omitted/default representation. The object is a complete replacement, not a field merge. For example, preserving custom DNS requires sending both `"dns_mode":"custom"` and the complete `dns_servers` list in the same object, while switching to system DNS can replace them with `"dns_mode":"system"`. When all other fields remain unchanged, changing only `client_listen_port`, `mtu`, a valid DNS setting, or `persistent_keepalive` updates the generated client config without moving the peer to another interface. If interface-level parameters differ, the peer is moved to the appropriate interface (created on demand if needed). Any change to the stored `port` value is rejected with `409` while the current profile has multiple peers, including switching between zero and its actual port; retry after the client is the profile's only peer.

For `routing`, an object replaces the complete policy and `null` resets it to full tunnel. Routing-only updates never move the peer or change its server-side `/32`; download and reapply the regenerated configuration on the client device for the new routing policy to take effect. The same re-download/reapply requirement applies to other client-only values.

The request body is limited to 1 MiB and must contain exactly one JSON value. A larger, malformed, trailing-garbage, or multiple-value body is rejected with `400 Bad Request` before client state changes. Unknown fields are ignored, but a body with none of the three recognized top-level fields is still an empty update.

For a client-only update that needs no migration, the prospective JSON is saved before the in-memory record is replaced. Before an interface-level update, the usage collector takes the same required complete snapshot used by H/S regeneration; a failed, malformed, or incomplete dump returns a generic `500` before pool mutation. The peer is then migrated, the prospective JSON is saved, and only then is memory committed. If persistence fails after migration, the server attempts a reverse migration and returns a generic `500`. Device and persistence rollback is best-effort rather than an absolute crash-atomic guarantee; logs and live interfaces must be inspected if rollback also fails.

**Response** `200 OK`:

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "protocol_version": "3.1",
  "address": "10.0.0.2",
  "lan_group_id": "peer:550e8400-e29b-41d4-a716-446655440000",
  "created_at": "2026-01-01T00:00:00Z",
  "awg_params": {
    "dns_mode": "custom",
    "dns_servers": ["1.1.1.1", "1.0.0.1"]
  },
  "routing": {
    "mode": "split",
    "allowed_ips": ["10.0.0.0/8"],
    "excluded_ips": ["10.20.0.0/16"]
  }
}
```

**Errors:**

- `400` — invalid request body, no recognized field, invalid/null/non-string `protocol_version`, invalid `awg_params`, incompatible version migration, or invalid `routing`
- `401` — bearer token missing or invalid
- `404` — client not found
- `409` — requested port is already in use, a shared profile rejects a port change, or the requested explicit port differs from an existing profile interface
- `503` — maximum number of interfaces reached
- `500` — the required pre-migration usage snapshot, device migration, or `clients.json` persistence failed; the response is generic and rollback is best-effort

## Regenerate Client AWG Parameters

```http
POST /api/clients/{id}/regenerate-awg-params
Authorization: Bearer <AWG_API_TOKEN>
```

This bearer-authenticated endpoint is defined without a request body, and any
supplied body is ignored. It validates the resulting effective profile,
migrates the peer through the interface pool, persists the replacement, and
returns the normal public client response shape. Generation is version-aware:
2.0 regenerates the legacy ranged H1-H4/S1-S2 fragment; 3.1 regenerates fixed
H1-H4/S1-S4 and a new private header-protection-key reference.

**Response** `200 OK` (`Content-Type: application/json`):

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "protocol_version": "3.1",
  "address": "10.0.0.2",
  "lan_group_id": "peer:550e8400-e29b-41d4-a716-446655440000",
  "created_at": "2026-01-01T00:00:00Z",
  "awg_params": {
    "port": 51825,
    "client_listen_port": 54321,
    "mtu": 1280,
    "dns_mode": "custom",
    "dns_servers": ["9.9.9.9", "149.112.112.112"],
    "persistent_keepalive": "25-35",
    "jc": 8,
    "jmin": 50,
    "jmax": 1000,
    "s1": 15,
    "s2": 16,
    "s3": 15,
    "s4": 12,
    "h1": "123456",
    "h2": "1234567",
    "h3": "12345678",
    "h4": "123456789"
  },
  "routing": {
    "mode": "full"
  }
}
```

For 2.0, regeneration replaces H1-H4 and S1/S2 only. For 3.1, it replaces
H1-H4 and S1-S4 and stages a new header-protection-key reference. Both modes
preserve their canonical version, server port, client listen port, MTU, DNS,
persistent keepalive, Jc/Jmin/Jmax, I1-I5, routing, client ID, address,
creation time, identity keys, and peer PSK. A replaced non-default 3.1 header
key is removed from prospective state before Save; the new key map becomes
authoritative only after a successful save, while save failure retains the
previous map.

Immediately before peer migration, while the client manager write lock is held, the usage collector takes a complete snapshot of every active interface. Periodic and manual collections are serialized with this snapshot, and the same guard remains held through migration, persistence, and any reverse migration, so the last counters from the old peer are accumulated before its kernel state is removed. A failed dump command, malformed peer row, or active interface returning no peers makes the required snapshot fail; the action returns a generic `500` before pool mutation and leaves the stored client unchanged. The snapshot updates in-memory totals immediately; `usage.json` is written by the next scheduled or shutdown save rather than by the action itself.

After a successful per-client regeneration, the old client configuration no
longer matches the server-side profile. Fetch `GET /api/clients/{id}/configuration`
immediately and re-import the returned configuration. The action does not
rotate the client identity private key, peer PSK, address, routing, or protocol
version.

Errors use the common [JSON error envelope](#error-handling).

| Status | Meaning |
| ------ | ------- |
| `200` | The client was migrated and the updated normal client response is returned. |
| `400` | The resulting effective AWG profile is invalid. |
| `401` | The bearer token is missing or invalid. |
| `404` | The client was not found. |
| `409` | The preserved server port conflicts with another interface, including the shared explicit-port case described below. |
| `503` | The interface limit prevents migration. |
| `500` | Secure randomness, distinct-parameter generation, the required usage snapshot, device migration, or `clients.json` persistence failed. The response is generic. |

The explicit shared-port conflict is a `409`: when a client has a fixed server-side `port` and shares its current interface, the new H/S grouping key needs a different interface while that same port is still occupied by the shared old interface.

A failed migration never commits or persists the regenerated override. If migration succeeds but saving fails, the manager attempts a reverse migration before returning `500`. Pool failures similarly attempt to restore the old peer and route and remove the new peer. These rollback paths update bookkeeping for successful steps, but they cannot guarantee pristine live state after a rollback failure or process crash; the stored client remains unchanged while the host may require inspection or a service restart.

## Get Client Configuration

```http
GET /api/clients/{id}/configuration
```

**Response** `200 OK` (`Content-Type: text/plain`):

```ini
[Interface]
PrivateKey = <client-private-key>
ListenPort = 54321
Address = 10.0.0.2/32
DNS = 9.9.9.9, 149.112.112.112
MTU = 1280
Jc = 5
Jmin = 50
Jmax = 1000
S1 = 15
S2 = 16
S3 = 15
S4 = 12
H1 = 123456
H2 = 1234567
H3 = 12345678
H4 = 123456789
ContentPaddingAddition = 10-100
RekeyAfterTime = 100-120
RekeyTimeout = 3-7
RejectAfterTime = 150-180
KeepaliveTimeout = 5-15
MaxHandshakeAttempts = 15-20
RandomTrailers = on
DisableCookies = off
HeaderProtectionKey = <private-32-byte-key>

[Peer]
PublicKey = <server-public-key>
PresharedKey = <peer-preshared-key>
Endpoint = vpn.example.invalid:51820
AllowedIPs = 10.0.0.0/24, 0.0.0.0/0, ::/0
PersistentKeepalive = 25-35
```

This is a synthetic 3.1 example. A 2.0 configuration has neither
`HeaderProtectionKey` nor the six 3.1 range settings/toggles, and retains the
legacy H/S form. The server never writes a synthetic `ProtocolVersion` line.

`ListenPort` is included only when `awg_params.client_listen_port` is between
1024 and 65535; it is local to the client and does not change the server
`Endpoint` port. The MTU is the client override or the target version's
default (`AWG_MTU` for 2.0 and `AWG31_MTU` for 3.1). DNS uses `AWG_DNS` for
inherited/default mode, one address for the legacy override, or normalized
`dns_servers` for custom mode; system mode omits `DNS`. For 2.0 persistent
keepalive is a scalar integer (omission inherits 25 and zero disables it); 3.1
uses its configured scalar/range/`off` value. `PresharedKey` is generated by
the server for new clients and legacy records without it remain valid. The
Endpoint matches the interface selected by the effective profile. The first
`AllowedIPs` entry is always the canonical `AWG_ADDRESS` network.

**Errors:**

- `401` — bearer token missing or invalid
- `404` — client not found
- `500` — the client exists in memory but its interface/profile cannot be resolved or its persisted routing cannot be rendered; the response is generic

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

Returns accumulated traffic counters and last handshake time. Returns zeros if the client has never connected. `last_handshake` is omitted if no handshake occurred. This read does not trigger a live device poll: the collector runs once on startup and then every 60 seconds, so normal responses can lag current kernel counters by up to one collection interval.

Totals are stored in `{AWG_DATA_DIR}/usage.json` with a temporary-file-and-rename write. The collector saves after its startup collection, after each 60-second collection, and during graceful shutdown. A regeneration snapshot updates in-memory totals immediately but is not itself an immediate disk save. A process crash can therefore lose the most recent unsaved interval even though previously saved totals survive restart.

**Errors:**

- `401` — bearer token missing or invalid
- `404` — client not found

## Delete Client

```http
DELETE /api/clients/{id}
```

**Response** `204 No Content`

Deletion first installs the DROP-only LAN chain, removes the AWG peer and its `/32` route, destroys the interface when it becomes empty, saves a prospective `clients.json`, removes the in-memory client and its usage entry, and rebuilds same-group allows. Route deletion or final interface destruction failure attempts to restore the peer and route and returns a generic `500`. If persistence fails after device removal, the server attempts to add the peer back. A final firewall failure occurs after the deletion is committed and leaves LAN traffic blocked. Rollback is best-effort; a second failure can leave live kernel state requiring operator inspection.

**Errors:**

- `401` — bearer token missing or invalid
- `404` — client not found
- `500` — peer/route/interface removal or `clients.json` persistence failed; the response is generic

## Routing Object

`routing` is a top-level client field, separate from `awg_params`. It controls the portion of `AllowedIPs` rendered after the mandatory VPN network. IPv4 routes follow this model:

```text
AllowedIPs = VPNNetwork + (base(mode, allowed_ips) - excluded_ips)
```

| Mode | `allowed_ips` | `excluded_ips` | Generated behavior |
| ---- | ------------- | -------------- | ------------------ |
| `full` | Empty | Empty | VPN network, then `0.0.0.0/0, ::/0` |
| `bypass` | Empty | One or more IPv4 CIDRs | VPN network, then IPv4 complement plus `::/0` |
| `split` | One or more IPv4 CIDRs | Empty | VPN network, then existing ordered split behavior |
| `split` | One or more IPv4 CIDRs | Optional | VPN network, then included IPv4 set minus exclusions |

Omitted routing, `null`, and explicit `{"mode":"full"}` are equivalent and are canonically persisted without a `routing` field. `bypass` uses all IPv4 addresses as its base and requires at least one exclusion. `split` requires at least one included IPv4 CIDR. `full` rejects either non-empty list, and `bypass` rejects `allowed_ips`. Missing or unknown modes and invalid mode/list combinations return `400 Bad Request` before client state changes.

Both supplied lists accept IPv4 CIDRs only. Malformed CIDRs and IPv6 CIDRs return `400 Bad Request`; private, overlapping, and default IPv4 CIDRs are otherwise valid. Each list is limited to 4,096 supplied entries. Every prefix is masked to its network and exact duplicates are removed while preserving first-occurrence order before that normalized caller intent is persisted and returned. For example, `10.1.2.3/8`, `10.0.0.0/8`, `192.168.1.7/24` becomes `10.0.0.0/8`, `192.168.1.0/24`.

When exclusions are present, rendering sorts and merges the IPv4 base and exclusion ranges, subtracts the exclusions, and emits the deterministic smallest exact set of CIDRs in ascending address order. Exclusions outside the base are accepted and have no effect. A subtraction that removes the complete IPv4 base returns `400 Bad Request`, as does a computed result larger than 16,384 IPv4 CIDRs.

For example, this normalized combined split policy:

```json
{
  "mode": "split",
  "allowed_ips": ["10.0.0.0/8", "172.16.0.0/12"],
  "excluded_ips": ["10.20.0.0/16"]
}
```

renders:

```ini
AllowedIPs = 10.100.0.0/24, 10.0.0.0/12, 10.16.0.0/14, 10.21.0.0/16, 10.22.0.0/15, 10.24.0.0/13, 10.32.0.0/11, 10.64.0.0/10, 10.128.0.0/9, 172.16.0.0/12
```

`bypass` appends `::/0` after its computed IPv4 complement, so exclusions affect IPv4 only. `split` never adds an implicit IPv6 route. Split routing without exclusions retains its existing normalized, first-occurrence order rather than sorting the list.

API responses expose the normalized persisted intent, not the expanded computed complement. Routing changes affect only the generated client configuration: fetch and reapply `GET /api/clients/{id}/configuration` after every routing change. Routing never changes interface grouping, peer migration, or the server-side peer `/32`. Domain, application, and geosite routing require separate client-side logic and are not accepted by this CIDR-only contract.

## AWG Params Object

All fields are optional. The target `protocol_version` is resolved before
inheritance and validation. Unless documented otherwise below, zero integer
values and empty strings inherit the corresponding target-version default.
Validation runs both on the raw override and the effective profile before key
generation, address allocation, device work, or persistence.

| Field | Type | Description |
| ----- | ---- | ----------- |
| `port` | int | UDP listen port for the interface, inclusive range 1024-65535. If omitted or zero, auto-assigned from the base port. Used in client config `Endpoint`. On create, a grouping profile can be shared with port 0 or its actual port; another explicit port returns `409`. PATCH rejects any stored port change while the profile is shared. Every explicit port must be allowed by the host firewall. |
| `client_listen_port` | int | Local UDP listen port for the generated client `[Interface]`, inclusive range 1024-65535. If omitted or zero, `ListenPort` is omitted and the client selects a port automatically. Does not affect server interface grouping or `Endpoint`. |
| `mtu` | int | MTU for this client's generated config, range 1280-1420. Omit or set to 0 to inherit `AWG_MTU` for 2.0 or `AWG31_MTU` for 3.1. Does not affect interface grouping. |
| `dns` | string | Legacy-compatible single IPv4 DNS override. Omit or set to an empty string to inherit `AWG_DNS`. Cannot be combined with `dns_mode` or `dns_servers`, even when explicitly empty. Does not affect interface grouping. |
| `dns_mode` | string | `default` inherits `AWG_DNS`; `custom` uses `dns_servers`; `system` omits the `DNS` line. Cannot be combined with legacy `dns`. Does not affect interface grouping. |
| `dns_servers` | string[] | Unique IPv4 addresses for `custom`; empty or omitted for `default` and `system`. Hostnames, URLs, CIDRs, empty entries, and IPv6 are rejected. |
| `persistent_keepalive` | number or string | Client `[Peer]` value. 2.0 accepts only an integer 0-65535 (omitted inherits 25; zero disables). 3.1 accepts the unsigned-16 scalar/range/`off` grammar below. It does not affect interface grouping. |
| `jc` | int | Junk packet count, range 0-128. Zero inherits the server default. |
| `jmin` | int | Junk packet minimum size, range 0-1280. Zero inherits the server default. When effective `jc > 0`, effective `jmin` must be positive and less than `jmax`. |
| `jmax` | int | Junk packet maximum size, range 0-1280. Zero inherits the server default. When effective `jc > 0`, effective `jmax` must be positive and greater than `jmin`. |
| `s1` | int | Init packet padding, range 0-1132. Zero inherits the server default. |
| `s2` | int | Response packet padding, range 0-1188. Zero inherits the server default; effective `s2` must not equal `s1 + 56`. |
| `s3` | int | Underload packet padding, range 0-64. Zero inherits the server default; every effective 3.1 S value is at least 12. |
| `s4` | int | Transport packet padding, range 0-32. Zero inherits the server default; every effective 3.1 S value is at least 12. |
| `h1` - `h4` | string | Unsigned decimal `uint32` values or inclusive `start-end` ranges. Empty values inherit server defaults; all four effective ranges are required and must not overlap. 3.1 generation uses fixed values, while valid explicit ranges remain supported. |
| `i1` - `i5` | string | Tag-only CPS strings. Supported tags are `<b 0xHEX>`, `<t>`, `<r N>`, `<rc N>`, and `<rd N>`; `N` is 0-1000 and each expanded packet is at most 1280 bytes. Empty values inherit server defaults. |
| `content_padding_addition` | number or string | 3.1 only: unsigned-16 scalar, range, or `"off"`. |
| `rekey_after_time` | number or string | 3.1 only: unsigned-16 scalar, range, or `"off"`. |
| `rekey_timeout` | number or string | 3.1 only: unsigned-16 scalar, range, or `"off"`. |
| `reject_after_time` | number or string | 3.1 only: unsigned-16 scalar, range, or `"off"`. |
| `keepalive_timeout` | number or string | 3.1 only: unsigned-16 scalar, range, or `"off"`. |
| `max_handshake_attempts` | number or string | 3.1 only: unsigned-16 scalar, range, or `"off"`. |
| `random_trailers` | string | 3.1 only: exactly `on` or `off`. |
| `disable_cookies` | string | 3.1 only: exactly `on` or `off`. |

The unsigned-16 grammar is ASCII decimal `N`, `N-M`, or `off`. It rejects
whitespace, signed values, floats, exponent notation, empty values, reversed
ranges, and overflow. Scalars marshal as JSON numbers; ranges and `off` marshal
as JSON strings. A 2.0 request rejects every 3.1-only field and a ranged or
`off` `persistent_keepalive`.

Interface identity is an immutable profile, not `AWGParams.Key()`. It includes
protocol version, H1-H4, S1-S4, Jc/Jmin/Jmax, all server-applied 3.1
ranges/toggles, and the private 3.1 header-protection key. Requested port,
client listen port, MTU, DNS, persistent keepalive, I1-I5, routing, and peer
PSKs stay outside that identity.

### DNS Settings

The legacy `dns` field remains supported, while new clients can select an explicit mode. The accepted shapes and generated behavior are:

| Form | Accepted fields | Generated `[Interface]` behavior |
| ---- | --------------- | -------------------------------- |
| Omitted settings | All DNS fields omitted | `DNS = <AWG_DNS>` |
| Legacy inherited | `"dns":""` supplied alone | `DNS = <AWG_DNS>`; the empty override is canonically omitted |
| Legacy custom | `"dns":"9.9.9.9"` supplied alone | `DNS = 9.9.9.9` |
| `default` mode | `"dns_mode":"default"`; `dns_servers` omitted or empty | `DNS = <AWG_DNS>` |
| `custom` mode | `"dns_mode":"custom"` and one or more `dns_servers` | One comma-and-space-separated line, for example `DNS = 1.1.1.1, 1.0.0.1` |
| `system` mode | `"dns_mode":"system"`; `dns_servers` omitted or empty | No `DNS` line is generated |

Field presence is validated strictly. Supplying legacy `dns` forbids both `dns_mode` and `dns_servers`, including combinations such as `"dns":""` with a mode or list. Supplying `dns_servers` requires `dns_mode` even when the list is explicitly empty. An explicitly empty `dns_mode` is invalid. JSON `null` also counts as an explicitly supplied nested DNS field and follows the same presence rules. `custom` requires a non-empty list; `default` and `system` reject non-empty lists.

Every non-empty legacy `dns` value and every `dns_servers` item must be a plain IPv4 address. DoH URLs, hostnames, CIDRs, IPv6 addresses, and mixed-format values are rejected with `400 Bad Request`. Custom server addresses are canonicalized and duplicates are removed stably before persistence, preserving the first occurrence order.

Custom mode renders a single comma-separated line:

```ini
[Interface]
PrivateKey = <base64>
Address = 10.0.0.2/32
DNS = 1.1.1.1, 1.0.0.1
MTU = 1420
```

System mode omits the `DNS` line completely:

```ini
[Interface]
PrivateKey = <base64>
Address = 10.0.0.2/32
MTU = 1420
```

All legacy and mode-based DNS fields are client-only and remain outside interface grouping. Fetch and reapply the generated client configuration after changing them.

Malformed ranges, overlapping effective header ranges, unsupported CPS tags, text outside CPS tags, control characters, and expanded CPS packets larger than 1280 bytes are rejected with `400 Bad Request` before client state is changed.

## Error Handling

All handler-generated API error responses use `Content-Type: application/json` and the same envelope:

```json
{"error":"<message>"}
```

Mux-generated unknown-path `404` and wrong-method `405` responses are standard plain text instead. Endpoint sections define which statuses each action can return. The handler message contract is:

| Status | Message contract |
| ------ | ---------------- |
| `400 Bad Request` | A field- or request-specific validation message. |
| `401 Unauthorized` | Missing `Authorization` header: fixed `{"error":"missing authorization header"}`. Invalid bearer scheme or token: fixed `{"error":"invalid token"}`. |
| `404 Not Found` | An action-specific not-found message, such as `{"error":"client not found"}`. |
| `409 Conflict` | An action-specific duplicate, port, or shared-interface conflict message. |
| `503 Service Unavailable` | An action-specific capacity message, including the maximum-interface limit. |
| `500 Internal Server Error` | Always the fixed generic `{"error":"internal server error"}`; key generation, capacity-independent device/network failures, persistence failures, and rollback failures are logged but never returned to the caller. |

Messages for `400`, `404`, `409`, and `503` can include field or operation context. Clients should use the HTTP status for control flow instead of matching those dynamic strings.
