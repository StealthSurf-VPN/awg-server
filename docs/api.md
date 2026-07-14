# API Reference

Base URL: `http://<server_ip>:<AWG_HTTP_PORT>`

All endpoints require header: `Authorization: Bearer <AWG_API_TOKEN>` (except `/health`).

## HTTP Routing, Authentication, and Bodies

The server uses the Go method-aware `net/http` `ServeMux`. These are the complete registered routes:

| Method | Path | Authentication | Success | Handler errors |
| ------ | ---- | -------------- | ------- | -------------- |
| `GET` | `/health` | None | `200` | None |
| `GET` | `/api/clients` | Bearer | `200` | `401` |
| `POST` | `/api/clients` | Bearer | `201` | `400`, `401`, `409`, `503`, `500` |
| `PATCH` | `/api/clients/{id}` | Bearer | `200` | `400`, `401`, `404`, `409`, `503`, `500` |
| `GET` | `/api/clients/{id}/configuration` | Bearer | `200` | `401`, `404`, `500` |
| `GET` | `/api/clients/{id}/stats` | Bearer | `200` | `401`, `404` |
| `DELETE` | `/api/clients/{id}` | Bearer | `204` | `401`, `404`, `500` |
| `POST` | `/api/awg-params/generate` | Bearer | `200` | `401`, `500` |
| `POST` | `/api/clients/{id}/regenerate-awg-params` | Bearer | `200` | `400`, `401`, `404`, `409`, `503`, `500` |

There is no `GET /api/clients/{id}` endpoint. A method that does not match an otherwise known path receives the mux's standard `405 Method Not Allowed`; an unknown path receives its standard `404 Not Found`. Those mux-generated responses are plain text and do not use the API JSON error envelope. A registered `GET` pattern also serves `HEAD`, with the same authentication requirement. No CORS or custom `OPTIONS` route is registered.

The bearer check runs before a matched protected handler. A missing `Authorization` header returns `401` with `{"error":"missing authorization header"}`; a non-Bearer scheme or wrong token returns `401` with `{"error":"invalid token"}`.

`POST /api/clients` and `PATCH /api/clients/{id}` read at most 1 MiB and require exactly one JSON value. Empty, malformed, oversized, trailing-garbage, and multiple-value bodies return `400` before manager mutation. Unknown JSON fields are ignored; consequently, a PATCH containing no recognized top-level field still returns the empty-update `400`. The server does not require a request `Content-Type`, although clients should send `application/json`. All other handlers ignore the request body, including the two POST action endpoints.

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

This bearer-authenticated endpoint is defined without a request body. The handler does not read the body, so supplied bytes are ignored. It generates a standalone H/S fragment with the server's secure-random algorithm and does not read, mutate, or persist any server or client state.

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

Errors use the common [JSON error envelope](#error-handling).

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

If `awg_params` is omitted, the client uses server defaults (global `AWG_MTU`, global `AWG_DNS`, `PersistentKeepalive = 25`, auto-generated H/S, and env Jc/Jmin/Jmax). Per-client params are merged over defaults. A custom server-side `port` must be in the inclusive range 1024-65535; omitted or zero uses automatic server interface assignment. On create, a new peer can join an existing profile with port 0 or that interface's actual port. `client_listen_port` accepts the same range and adds `ListenPort` to the generated client `[Interface]`; omitted or zero leaves client-side port selection automatic. DNS supports the backward-compatible `dns` field and the mode-based `dns_mode`/`dns_servers` fields described in [DNS Settings](#dns-settings). `persistent_keepalive` accepts 0-65535: omission inherits 25, while an explicit zero disables keepalive.

If `routing` is omitted, `null`, or `{"mode":"full"}`, the client uses full-tunnel routing. See [Routing Object](#routing-object) for split-tunnel behavior and validation.

Every new client automatically receives a unique server-generated 32-byte preshared key. The API does not accept a PSK in the request and does not expose it in list, create, or update JSON responses. It is returned only as part of the authenticated client configuration.

The request body is limited to 1 MiB and must contain exactly one JSON value. A larger body, malformed JSON, trailing garbage, or a second JSON value is rejected as an invalid request with `400 Bad Request` before client state changes. Unknown fields are ignored. The `id` must be non-empty and no longer than 256 Unicode characters.

Creation stages the interface/peer/route first, saves a prospective `clients.json`, and only then commits the client to the in-memory map. A device failure or persistence failure returns the generic `500`; after a save failure the server attempts to remove the staged peer and any now-empty interface. That rollback is best-effort: if it also fails, the persisted and in-memory client remain uncommitted, but live kernel state may require operator cleanup.

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

- `400` — malformed request body, missing or invalid `id`, id too long (max 256 Unicode characters), invalid `awg_params`, or invalid `routing`
- `401` — bearer token missing or invalid
- `409` — client with this id already exists, requested port is already in use, or an existing profile uses a different actual port
- `503` — maximum number of interfaces reached
- `500` — key generation, IP allocation, device/network setup, or `clients.json` persistence failed; the response is generic

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

Updates `awg_params` and `routing` independently. Omitting either field preserves its current value, JSON `null` resets that field to its default behavior, and an object replaces the complete stored value for that field. A request containing neither field returns `400 Bad Request`.

For `awg_params`, include every custom field that must be retained; `null` reverts all fields to their automatic or server-default behavior. An empty object is normalized to the same omitted/default representation. The object is a complete replacement, not a field merge. For example, preserving custom DNS requires sending both `"dns_mode":"custom"` and the complete `dns_servers` list in the same object, while switching to system DNS can replace them with `"dns_mode":"system"`. When all other fields remain unchanged, changing only `client_listen_port`, `mtu`, a valid DNS setting, or `persistent_keepalive` updates the generated client config without moving the peer to another interface. If interface-level parameters differ, the peer is moved to the appropriate interface (created on demand if needed). Any change to the stored `port` value is rejected with `409` while the current profile has multiple peers, including switching between zero and its actual port; retry after the client is the profile's only peer.

For `routing`, an object replaces the complete policy and `null` resets it to full tunnel. Routing-only updates never move the peer or change its server-side `/32`; download and reapply the regenerated configuration on the client device for the new routing policy to take effect. The same re-download/reapply requirement applies to other client-only values.

The request body is limited to 1 MiB and must contain exactly one JSON value. A larger, malformed, trailing-garbage, or multiple-value body is rejected with `400 Bad Request` before client state changes. Unknown fields are ignored, but a body with neither recognized top-level field is still an empty update.

For a client-only update that needs no migration, the prospective JSON is saved before the in-memory record is replaced. Before an interface-level update, the usage collector takes the same required complete snapshot used by H/S regeneration; a failed, malformed, or incomplete dump returns a generic `500` before pool mutation. The peer is then migrated, the prospective JSON is saved, and only then is memory committed. If persistence fails after migration, the server attempts a reverse migration and returns a generic `500`. Device and persistence rollback is best-effort rather than an absolute crash-atomic guarantee; logs and live interfaces must be inspected if rollback also fails.

**Response** `200 OK`:

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "address": "10.0.0.2",
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

- `400` — invalid request body, neither supported field supplied, invalid `awg_params`, or invalid `routing`
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

This bearer-authenticated endpoint is defined without a request body, and any supplied body is ignored. It generates a new H1-H4 and S1-S2 set for the client, validates the resulting effective profile, migrates the peer through the existing interface pool, persists the replacement, and returns the normal client response shape.

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

Immediately before peer migration, while the client manager write lock is held, the usage collector takes a complete snapshot of every active interface. Periodic and manual collections are serialized with this snapshot, and the same guard remains held through migration, persistence, and any reverse migration, so the last counters from the old peer are accumulated before its kernel state is removed. A failed dump command, malformed peer row, or active interface returning no peers makes the required snapshot fail; the action returns a generic `500` before pool mutation and leaves the stored client unchanged. The snapshot updates in-memory totals immediately; `usage.json` is written by the next scheduled or shutdown save rather than by the action itself.

After a successful per-client regeneration, the old client configuration no longer matches the server-side H1-H4/S1-S2 profile. Fetch `GET /api/clients/{id}/configuration` immediately and reapply the returned configuration. The action does not rotate the private key, preshared key, address, or any unrelated AWG setting.

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
S3 = 0
S4 = 0
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

Deletion removes the AWG peer and its `/32` route, destroys the interface when it becomes empty, saves a prospective `clients.json`, and only then removes the in-memory client and its usage entry. Route deletion or final interface destruction failure attempts to restore the peer and route and returns a generic `500`. If persistence fails after device removal, the server attempts to add the peer back. Rollback is best-effort; a second failure can leave live kernel state requiring operator inspection even though the stored and in-memory client were not committed as deleted.

**Errors:**

- `401` — bearer token missing or invalid
- `404` — client not found
- `500` — peer/route/interface removal or `clients.json` persistence failed; the response is generic

## Routing Object

`routing` is a top-level client field, separate from `awg_params`. It controls the `AllowedIPs` line rendered in the generated client configuration. IPv4 routes follow this model:

```text
AllowedIPv4 = base(mode, allowed_ips) - excluded_ips
```

| Mode | `allowed_ips` | `excluded_ips` | Generated behavior |
| ---- | ------------- | -------------- | ------------------ |
| `full` | Empty | Empty | `0.0.0.0/0, ::/0` |
| `bypass` | Empty | One or more IPv4 CIDRs | IPv4 complement plus `::/0` |
| `split` | One or more IPv4 CIDRs | Empty | Existing ordered split behavior |
| `split` | One or more IPv4 CIDRs | Optional | Included IPv4 set minus exclusions |

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
AllowedIPs = 10.0.0.0/12, 10.16.0.0/14, 10.21.0.0/16, 10.22.0.0/15, 10.24.0.0/13, 10.32.0.0/11, 10.64.0.0/10, 10.128.0.0/9, 172.16.0.0/12
```

`bypass` appends `::/0` after its computed IPv4 complement, so exclusions affect IPv4 only. `split` never adds an implicit IPv6 route. Split routing without exclusions retains its existing normalized, first-occurrence order rather than sorting the list.

API responses expose the normalized persisted intent, not the expanded computed complement. Routing changes affect only the generated client configuration: fetch and reapply `GET /api/clients/{id}/configuration` after every routing change. Routing never changes interface grouping, peer migration, or the server-side peer `/32`. Domain, application, and geosite routing require separate client-side logic and are not accepted by this CIDR-only contract.

## AWG Params Object

All fields are optional. Unless documented otherwise below, zero integer values and empty strings inherit the corresponding server default. Validation is performed both on the supplied override and on the effective profile after inheritance.

| Field | Type | Description |
| ----- | ---- | ----------- |
| `port` | int | UDP listen port for the interface, inclusive range 1024-65535. If omitted or zero, auto-assigned from the base port. Used in client config `Endpoint`. On create, a grouping profile can be shared with port 0 or its actual port; another explicit port returns `409`. PATCH rejects any stored port change while the profile is shared. Every explicit port must be allowed by the host firewall. |
| `client_listen_port` | int | Local UDP listen port for the generated client `[Interface]`, inclusive range 1024-65535. If omitted or zero, `ListenPort` is omitted and the client selects a port automatically. Does not affect server interface grouping or `Endpoint`. |
| `mtu` | int | MTU for this client's generated config, range 1280-1420. Omit or set to 0 to inherit `AWG_MTU`. Does not affect interface grouping. |
| `dns` | string | Legacy-compatible single IPv4 DNS override. Omit or set to an empty string to inherit `AWG_DNS`. Cannot be combined with `dns_mode` or `dns_servers`, even when explicitly empty. Does not affect interface grouping. |
| `dns_mode` | string | `default` inherits `AWG_DNS`; `custom` uses `dns_servers`; `system` omits the `DNS` line. Cannot be combined with legacy `dns`. Does not affect interface grouping. |
| `dns_servers` | string[] | Unique IPv4 addresses for `custom`; empty or omitted for `default` and `system`. Hostnames, URLs, CIDRs, empty entries, and IPv6 are rejected. |
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
