# User Network Settings and AWG Parameter Regeneration

## Status

Approved in conversation on 2026-07-14. This document defines the implementation contract for combined client routing, DNS selection, and AWG H/S parameter generation.

## Context

`awg-server` already supports per-client AWG parameter overrides and two routing policies:

- `full`, rendered as `0.0.0.0/0, ::/0`
- `split`, rendered from a caller-supplied list of IPv4 CIDRs

It also accepts one legacy IPv4 DNS override in `awg_params.dns` and has an internal cryptographic generator for H1-H4 and S1-S2 values. The StealthSurf backend needs a richer low-level contract so it can own product presets while passing explicit settings to `awg-server`.

## Goals

1. Add bypass routing and support exclusions on top of split routing.
2. Add explicit DNS modes and multiple DNS servers while preserving the legacy `dns` field.
3. Expose pure AWG H/S generation and an action that regenerates and immediately applies those values to one client.
4. Preserve existing clients, persisted JSON, request semantics, interface grouping, and authentication.
5. Document every new field, combination, validation rule, generated configuration effect, and operational consequence.

## Non-Goals

- Product-level masking profiles or presets. The StealthSurf backend owns those.
- Domain, application, geosite, or process routing.
- IPv6 CIDRs in `allowed_ips` or `excluded_ips`.
- DNS-over-HTTPS URLs, hostnames, or DNS reachability checks.
- Private key, preshared key, client address, I1-I5, Jc/Jmin/Jmax, S3/S4, port, MTU, or keepalive regeneration.
- Regeneration of the server-wide persisted default H/S values.
- API versioning or a new HTTP framework.

## Compatibility Requirements

- Omitted or `null` routing remains full tunnel.
- Explicit `{"mode":"full"}` remains canonically persisted as omitted routing.
- Existing `split` requests without `excluded_ips` keep their current meaning.
- Existing `awg_params.dns` values and persisted client records continue to work unchanged.
- `PATCH /api/clients/{id}` keeps replacement semantics for each supplied top-level object: callers must send every custom `awg_params` field they want to retain.
- New DNS fields and routing fields remain client-side configuration settings and do not affect the server interface grouping key.

## External API Contract

All new endpoints use the existing bearer authentication middleware and existing JSON error shape.

### Routing Object

The routing object gains `excluded_ips`:

```json
{
  "mode": "split",
  "allowed_ips": ["10.0.0.0/8", "172.16.0.0/12"],
  "excluded_ips": ["10.20.0.0/16"]
}
```

The mode chooses the base set. Exclusions are then subtracted from that set.

| Mode | `allowed_ips` | `excluded_ips` | IPv4 result | IPv6 result |
| ---- | ------------- | -------------- | ----------- | ----------- |
| `full` | Empty | Empty | `0.0.0.0/0` | `::/0` |
| `bypass` | Empty | One or more CIDRs | `0.0.0.0/0 - excluded_ips` | `::/0` |
| `split` | One or more CIDRs | Empty | Existing normalized `allowed_ips` behavior | None |
| `split` | One or more CIDRs | Zero or more CIDRs | `allowed_ips - excluded_ips` | None |

Invalid combinations return `400 Bad Request`:

- `full` with either non-empty list
- `bypass` without exclusions or with `allowed_ips`
- `split` without `allowed_ips`
- an unknown or missing mode in a non-null routing object
- malformed CIDRs or non-IPv4 CIDRs in either list
- a subtraction that leaves no IPv4 routes

`excluded_ips` entries outside the base set are valid and have no effect.

### DNS Fields

`awg_params` gains two optional fields while retaining `dns`:

```json
{
  "dns_mode": "custom",
  "dns_servers": ["1.1.1.1", "1.0.0.1"]
}
```

| Input | Requirements | Generated `[Interface]` behavior |
| ----- | ------------ | -------------------------------- |
| No DNS fields | Legacy default behavior | `DNS = <AWG_DNS>` |
| `dns: "9.9.9.9"` | One IPv4 address | `DNS = 9.9.9.9` |
| `dns_mode: "default"` | `dns_servers` empty or omitted | `DNS = <AWG_DNS>` |
| `dns_mode: "custom"` | One or more IPv4 addresses | One comma-separated `DNS` line |
| `dns_mode: "system"` | `dns_servers` empty or omitted | Omit the `DNS` line entirely |

Validation rules:

- Presence of the legacy `dns` field, including `dns: ""`, cannot be combined with `dns_mode` or `dns_servers`.
- DNS field presence follows Go's case-insensitive JSON field matching, so `DNS`, `DNS_MODE`, and `DNS_SERVERS` cannot bypass mixed-format or null/empty validation.
- Presence of `dns_servers`, including an explicit empty array, without `dns_mode` is invalid.
- `dns_mode` must be `default`, `custom`, or `system`.
- `custom` requires at least one server.
- `default` and `system` reject a non-empty server list.
- Every server must be an IPv4 address. IPv6, CIDRs, hostnames, URLs, and empty strings are invalid.
- Duplicate servers are removed while preserving first-occurrence order.

An explicit `dns_mode` is persisted and returned in client JSON. Legacy records are not rewritten solely because the server was upgraded.

### Generate AWG Parameters

```http
POST /api/awg-params/generate
```

The endpoint has no request body and does not mutate server or client state. It returns `200 OK` with a fragment suitable for insertion into `awg_params`:

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

It uses the existing `crypto/rand`-backed generation algorithm and returns `500 Internal Server Error` if secure randomness fails.

### Regenerate a Client's AWG Parameters

```http
POST /api/clients/{id}/regenerate-awg-params
```

The endpoint has no request body. It returns `200 OK` using the existing client response shape.

The action:

1. Loads the client while holding the manager write lock.
2. Copies its stored `awg_params`, or starts with an empty override object when it is currently omitted.
3. Generates and replaces only H1-H4 and S1-S2.
4. Preserves port, client listen port, MTU, all DNS fields, keepalive, Jc/Jmin/Jmax, S3/S4, I1-I5, routing, keys, address, and creation time.
5. Validates the resulting effective profile before changing the device.
6. Takes a required final usage snapshot while holding the manager write lock and keeps periodic/manual collection blocked through migration.
7. Migrates the peer through the existing interface pool path.
8. Updates in-memory and persisted client data only after successful migration, using the same persistence guarantees as the existing update path.

The client action must generate a grouping key different from the client's current effective grouping key. In the practically impossible event of an identical generated result, it makes up to eight generation attempts before returning an internal error.

After success, the previously issued client configuration no longer matches the server-side H/S profile. The caller must immediately fetch `GET /api/clients/{id}/configuration` and deliver/reapply the new configuration.

If a client uses an explicit server-side `port` and currently shares its interface, regeneration can return `409`: the new H/S grouping key requires another interface, but the explicitly requested port is still occupied by the shared old interface. The client remains unchanged in that case.

Errors follow existing manager mappings:

- `404` when the client does not exist
- `400` when the resulting effective AWG profile is invalid
- `409` for an interface port conflict or an unsupported port change on a shared interface
- `503` when the interface limit prevents migration
- `500` for secure randomness, required usage snapshot failure, or other internal device failures

## Routing Normalization and Subtraction

The API stores caller intent rather than the expanded complement. Both input arrays are independently normalized by masking each prefix to its network, removing exact duplicates, and retaining first-occurrence order.

When exclusions are present, rendering uses a deterministic interval algorithm:

1. Convert each normalized IPv4 prefix to a closed `uint32` interval.
2. Sort and merge overlapping or adjacent base intervals.
3. Sort and merge overlapping or adjacent exclusion intervals.
4. Subtract exclusions from the base intervals.
5. Convert each remaining interval to the smallest exact set of aligned IPv4 CIDRs.
6. Emit computed CIDRs in ascending address order.

The implementation must never enumerate individual IP addresses.

For compatibility, `split` without exclusions continues to render its normalized `allowed_ips` directly and in first-occurrence order. `full` continues to render the existing constant. `bypass` appends `::/0` after the computed IPv4 complement; `split` never adds an implicit IPv6 route.

The existing 1 MiB request-body limit remains in force. To bound generated configuration size, each supplied routing list accepts at most 4,096 entries and the computed IPv4 result accepts at most 16,384 CIDRs. Exceeding either limit returns a field-specific `400 Bad Request`. These limits are part of the public API contract and must be documented.

## Internal Design

### `internal/clients`

- Extend `Routing` with `ExcludedIPs []string` and add `RoutingModeBypass`.
- Keep normalization and route computation in this package so the dependency direction remains unchanged.
- Add pure helpers for interval merging, subtraction, and exact interval-to-CIDR conversion.
- Keep normalized intent in `ClientData.Routing`; compute the final `AllowedIPs` only for configuration rendering.
- Add a manager method for client regeneration rather than composing `GetClient` and `UpdateClient`, which would introduce a race between calls.
- Reuse a locked internal update/migration path so regular updates and regeneration share validation, migration, response, and persistence behavior.

### `internal/awg`

- Extend `AWGParams` with `DNSMode string` and `DNSServers []string` JSON fields.
- Keep `DNS string` for backward compatibility.
- Preserve JSON field-presence information during request decoding so an explicitly supplied empty legacy `dns` or empty `dns_servers` can still participate in mixed-format validation. Presence markers are internal and never serialized.
- Match DNS presence markers case-insensitively, consistent with `encoding/json` field matching.
- Add pure validation/normalization for the legacy and new DNS forms.
- Add a pure helper that applies `GeneratedParams` to a deep-cloned override object while preserving every unrelated field and slice.
- Keep `Key()`, `CLIArgs()`, and `ConfigLines()` independent of DNS.
- Continue using the existing `GenerateParams()` implementation for both endpoints.

### `internal/api`

- Register both authenticated POST routes on the existing `http.ServeMux`.
- Add small handlers that delegate generation or regeneration and use existing response/error helpers.
- Normalize request data before persistence and retain manager-side validation as the authoritative boundary.
- Do not log generated configuration secrets or stored client keys.

### Configuration Rendering

DNS rendering must be decided from the stored override plus the server default, not merely from a non-empty legacy `DNS` string:

- inherited/default mode renders the configured `AWG_DNS`
- legacy mode renders `dns`
- custom mode joins normalized `dns_servers` with `, `
- system mode omits the complete `DNS = ...` line

Routing and DNS changes never migrate a peer by themselves. H/S regeneration always changes the grouping key and therefore follows the existing migration path.

## Concurrency and Failure Semantics

- Client regeneration is serialized with create, update, and delete by `Manager.mu`.
- Generation without a client is stateless and needs no manager lock.
- All input and effective-profile validation happens before interface mutation.
- Immediately before regeneration migrates the peer, the manager invokes a generic migration guard while still holding its write lock. The usage collector uses that guard to take a required all-interface snapshot and holds the same serialization lock through the pool migration.
- Every periodic or manual `Collector.Collect()` invocation uses the same serialization lock, so it cannot observe an intermediate migration state or race the required snapshot.
- A required snapshot failure is logged inside the usage package and returned across the API boundary only as a safe generic error. It aborts regeneration before pool mutation and produces a generic `500` response.
- A failed migration must not replace the client's stored AWG overrides. When removal from a shared old interface fails after the peer was added to the new interface, the pool best-effort restores the old peer and route, removes the new peer, and updates peer counts, interface maps, and used-port bookkeeping for every successful rollback step. Partial rollback failure is logged safely and still returns an error.
- Storage writes retain the current atomic temporary-file-and-rename implementation and the existing manager behavior for save failures; changing global persistence error semantics is outside this feature.
- Routing computation and DNS normalization are pure and must not mutate caller-owned slices.

## Documentation Deliverables

Documentation is part of the definition of done:

### `docs/api.md`

- Add both POST endpoints, request/response examples, status codes, and authentication behavior.
- Expand the routing truth table with `bypass` and `split` plus exclusions.
- Explain normalization, subtraction priority, IPv4-only input, IPv6 output behavior, limits, empty-result rejection, and reapplication requirements.
- Expand the AWG params table with `dns_mode` and `dns_servers`.
- Add the DNS compatibility/validation matrix and generated config examples for all three modes.
- State that regeneration invalidates the old client configuration immediately after success.

### `docs/configuration.md`

- Update `AWG_DNS` inheritance wording.
- Document the persisted shapes for legacy DNS, explicit DNS modes, bypass routing, and combined split routing.
- Reconfirm that DNS/routing do not affect interface grouping.
- Distinguish server-wide generated defaults from per-client regeneration.

### `README.md`

- Add concise curl examples for bypass routing, combined split routing, custom/system DNS, pure generation, and client regeneration.
- Link detailed semantics to `docs/api.md` rather than duplicating the full contract.

## Verification Strategy

The accepted project policy for this feature does not require new automated tests. Do not add new `*_test.go` files. `go test ./...` remains a required compile pass across all packages and runs any tests that already exist, but this feature does not require new test cases.

Production behavior is verified through the existing compile/build/static-analysis checks, formatting and diff checks, and independent review of the implementation against this design. When Go files change, format them before running verification.

### Required verification

```bash
gofmt -w <changed-go-files>
go test ./...
go build -o awg-server .
go vet ./...
git diff --check
```

`gofmt` applies only to changed Go files. The generated `awg-server` build artifact is not committed. Each implementation task receives an independent specification-compliance and quality review, followed by a broad final review of the complete branch.

## Definition of Done

- All three feature areas match this contract and preserve old records.
- No new `*_test.go` files are added; the accepted compile, build, vet, gofmt, and diff checks pass.
- Build and vet pass.
- `docs/api.md`, `docs/configuration.md`, and `README.md` contain the promised examples and operational warnings.
- Independent task and whole-branch reviews report no unresolved critical or important findings.
- No secrets appear in source, fixtures, logs, or documentation.
