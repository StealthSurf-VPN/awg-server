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
| `GET` | `/api/capabilities` | Report the complete LAN-group isolation contract |
| `POST` | `/api/awg-params/generate` | Generate a stateless H1-H4/S1-S2 fragment |
| `GET` | `/api/clients` | List clients for backend reconciliation and orphan cleanup |
| `POST` | `/api/clients` | Create a client, optionally with `lan_group_id`, custom `awg_params`, and `routing` |
| `PATCH` | `/api/clients/lan-group` | Atomically replace LAN-group membership for a validated client batch |
| `PATCH` | `/api/clients/{id}` | Independently replace or reset the client's `awg_params` and `routing` |
| `POST` | `/api/clients/{id}/regenerate-awg-params` | Snapshot usage, regenerate H1-H4/S1-S2, and migrate the peer |
| `GET` | `/api/clients/{id}/configuration` | Return the generated `.conf` file |
| `GET` | `/api/clients/{id}/stats` | Return accumulated usage and last handshake |
| `DELETE` | `/api/clients/{id}` | Remove the client and any now-empty interface |

## Client Fields

- `POST /api/clients` accepts optional top-level `lan_group_id`, `awg_params`, and `routing`. Omitted `lan_group_id` becomes `peer:<id>`; omitted or null routing creates the backward-compatible full-tunnel policy.
- `GET /api/capabilities` returns exactly `{"lan_group_isolation":true}` after successful startup; the value guarantees persistence, create/batch contracts, fail-closed firewall isolation, and the explicit VPN CIDR in generated `AllowedIPs`.
- `PATCH /api/clients/lan-group` validates all unique IDs before installing the DROP-only gate or mutating state, saves the complete batch under the manager mutex, and returns `{"clients":[...]}` with the standard safe public client shape.
- `PATCH /api/clients/{id}` treats `awg_params` and `routing` independently: omission preserves a field, JSON null resets it, and an object replaces its complete stored value.
- `POST /api/clients`, `PATCH /api/clients/lan-group`, and `PATCH /api/clients/{id}` limit request bodies to 1 MiB; oversized bodies return `400 Bad Request` before mutation.
- Create and update bodies must contain exactly one JSON document; a second value, trailing garbage, or over-limit trailing data returns the same generic `400 Bad Request` before mutation.
- A PATCH body containing neither `awg_params` nor `routing` returns `400 Bad Request`; null counts as an explicitly supplied reset.
- List, create, and update responses return effective routing as `{"mode":"full"}`, normalized bypass intent, or normalized split intent, even when full routing is omitted from persistence.
- Routing-only updates do not migrate peers because routing is not part of `AWGParams` or interface grouping.
- Legacy `dns` and mode-based `dns_mode`/`dns_servers` are mutually exclusive under case-insensitive JSON field matching. DNS is client-only and never changes interface grouping.
- Every interface-level PATCH and client H/S regeneration takes a required serialized usage snapshot while the manager write lock is held and keeps collection blocked through migration. Snapshot failure returns generic `500` before pool mutation; any migration failure leaves the stored override unchanged and never returns success.

When the contract changes, update `docs/api.md` and any affected examples in `README.md` in the same patch.
