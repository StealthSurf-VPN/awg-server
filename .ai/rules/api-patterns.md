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
| `GET` | `/api/clients` | List clients for backend reconciliation and orphan cleanup |
| `POST` | `/api/clients` | Create a client, optionally with custom `awg_params` and `routing` |
| `PATCH` | `/api/clients/{id}` | Independently replace or reset the client's `awg_params` and `routing` |
| `GET` | `/api/clients/{id}/configuration` | Return the generated `.conf` file |
| `GET` | `/api/clients/{id}/stats` | Return accumulated usage and last handshake |
| `DELETE` | `/api/clients/{id}` | Remove the client and any now-empty interface |

## Client Fields

- `POST /api/clients` accepts optional top-level `awg_params` and `routing` objects. Omitted or null routing creates the backward-compatible full-tunnel policy.
- `PATCH /api/clients/{id}` treats `awg_params` and `routing` independently: omission preserves a field, JSON null resets it, and an object replaces its complete stored value.
- A PATCH body containing neither `awg_params` nor `routing` returns `400 Bad Request`; null counts as an explicitly supplied reset.
- List, create, and update responses return effective routing as `{"mode":"full"}` or a normalized split object, even when full routing is omitted from persistence.
- Routing-only updates do not migrate peers because routing is not part of `AWGParams` or interface grouping.

When the contract changes, update `docs/api.md` and any affected examples in `README.md` in the same patch.
