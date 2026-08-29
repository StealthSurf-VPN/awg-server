# API Patterns

## Routing and Authentication

- Use the Go 1.22+ method-aware `net/http` ServeMux in `internal/api/server.go`.
- Register new authenticated routes inside the bearer-authenticated API surface.
- Keep `GET /health` unauthenticated for monitoring; do not add other unauthenticated endpoints without explicit approval.
- Require `Authorization: Bearer <AWG_API_TOKEN>` before executing protected handlers.

## Handler Behavior

- Decode and validate input before mutating manager or pool state.
- Return immediately after writing an error response.
- Use JSON for structured responses and `text/plain` for generated client configuration files.
- Route internal failures through the shared error-writing path: log details server-side and return a generic message to callers.
- Preserve the established status-code contract:
  - `200` for successful list, get, update, configuration, and stats requests.
  - `201` for successful creation.
  - `204` for successful deletion.
  - `400` for malformed or invalid input.
  - `401` for missing or invalid bearer authentication.
  - `404` for a missing client.
  - `409` for duplicate clients, port conflicts, or an unsupported port change on a shared interface.
  - `503` when the interface limit prevents the operation.

## Current Contract

| Method | Path | Purpose |
| ------ | ---- | ------- |
| `GET` | `/health` | Unauthenticated health check |
| `GET` | `/api/capabilities` | Report qualified AWG 3.1 and LAN-group support |
| `POST` | `/api/awg-params/generate` | Generate a stateless public fragment for optional `?protocol_version=2|2.0|3.1` |
| `GET` | `/api/clients` | List clients for backend reconciliation and orphan cleanup |
| `POST` | `/api/clients` | Create a client with optional `protocol_version`, `lan_group_id`, custom `awg_params`, and `routing` |
| `PATCH` | `/api/clients/lan-group` | Atomically replace LAN-group membership for a validated client batch |
| `PATCH` | `/api/clients/{id}` | Preserve or explicitly migrate version and independently replace/reset `awg_params` and `routing` |
| `POST` | `/api/clients/{id}/regenerate-awg-params` | Snapshot usage, regenerate version-aware parameters, and migrate the peer |
| `GET` | `/api/clients/{id}/configuration` | Return the generated `.conf` file |
| `GET` | `/api/clients/{id}/stats` | Return accumulated usage and last handshake |
| `DELETE` | `/api/clients/{id}` | Remove the client and any now-empty interface |

## Client Fields

- `POST /api/clients` accepts optional top-level `protocol_version`,
  `lan_group_id`, `awg_params`, and `routing`. Omitted version uses the
  configured default; `2` normalizes to canonical `2.0`, while null/non-string/
  unknown values fail `400` before mutation. Omitted `lan_group_id` becomes
  `peer:<id>`; omitted or null routing creates the backward-compatible
  full-tunnel policy.
- `GET /api/capabilities` returns exactly
  `{"awg_protocol_3_1":true,"lan_group_isolation":true}` after qualified
  startup. The values guarantee runtime-qualified 3.1 support and the existing
  LAN persistence/create/batch/fail-closed firewall/VPN-CIDR contract.
- `PATCH /api/clients/lan-group` validates all unique IDs before installing the DROP-only gate or mutating state, saves the complete batch under the manager mutex, and returns `{"clients":[...]}` with the standard safe public client shape.
- `PATCH /api/clients/{id}` treats `protocol_version`, `awg_params`, and
  `routing` independently. Version omission preserves; explicit supported
  version can migrate; null is invalid. Params/routing omission preserves,
  null resets, and an object replaces its complete stored value against the
  target version.
- `POST /api/clients`, `PATCH /api/clients/lan-group`, and `PATCH /api/clients/{id}` limit request bodies to 1 MiB; oversized bodies return `400 Bad Request` before mutation.
- Create and update bodies must contain exactly one JSON document; a second value, trailing garbage, or over-limit trailing data returns the same generic `400 Bad Request` before mutation.
- A PATCH body containing none of `protocol_version`, `awg_params`, or
  `routing` returns `400 Bad Request`; null counts as a params/routing reset but
  is invalid for protocol version.
- Every public client response (list, create, update, regenerate, LAN group)
  includes canonical `protocol_version` and effective routing, but never a
  private header-key reference/key, client private key, public key, or PSK.
- Routing-only updates do not migrate peers because routing is not part of
  immutable profile identity.
- Legacy `dns` and mode-based `dns_mode`/`dns_servers` are mutually exclusive under case-insensitive JSON field matching. DNS is client-only and never changes interface grouping.
- Every protocol/interface-level PATCH and client regeneration takes a required
  serialized usage snapshot while the manager write lock is held and keeps
  collection blocked through migration. Snapshot failure returns generic `500`
  before pool mutation; migration failure leaves stored state unchanged and
  never returns success. 2.0 regeneration rotates legacy H1-H4/S1-S2; 3.1
  regenerates fixed H1-H4/S1-S4 and a private key reference.

When the contract changes, update `docs/api.md` and any affected examples in `README.md` in the same patch.
