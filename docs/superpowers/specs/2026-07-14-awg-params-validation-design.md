# AWG Parameter Validation Design

## Context

The API currently validates `port`, `mtu`, `dns`, and `persistent_keepalive`, but accepts CPS overrides without validating their ranges, relationships, or string formats. Invalid `J`, `S`, or `H` values can reach `awg set` and surface as HTTP 500 responses. Invalid `I1`-`I5` values are written directly into generated client configurations and may make those configurations invalid.

The public AmneziaWG parameter table is not a reliable source for every hard limit. In particular, it lists `Jmin` and `Jmax` as 64-1024, while the official Amnezia client generates `Jmin = 10`, the kernel module documentation recommends `Jmin = 8`, and both current implementations accept positive values below 64. This project will therefore use implementation-compatible constraints instead of enforcing that disputed lower bound.

## Goals

- Reject invalid CPS input before any key generation, IP allocation, peer migration, or `awg` invocation.
- Return HTTP 400 for invalid API input instead of allowing it to become an internal error.
- Validate the effective profile after request overrides are merged with server defaults.
- Reject malformed CPS strings before they are rendered into client configuration files.
- Preserve current defaults and previously generated `S1`/`S2` values.
- Fail startup clearly when operator-controlled default CPS settings are invalid.

## Non-goals

- Changing existing zero-as-inheritance semantics for integer API overrides.
- Migrating, rewriting, deleting, or newly rejecting persisted client records during startup.
- Changing the generated `J`, `S`, or `H` defaults.
- Adding new CPS tags or supporting implementation-specific tags that are not portable across clients.
- Adding Go test files, per the explicit project requirement for this change.

## Validation Contract

### Junk packets: `Jc`, `Jmin`, and `Jmax`

- `Jc` must be between 0 and 128 inclusive.
- `Jmin` and `Jmax` must each be between 0 and 1280 inclusive.
- When the effective `Jc` is greater than zero:
  - `Jmin` must be greater than zero.
  - `Jmax` must be greater than zero.
  - `Jmin` must be strictly less than `Jmax`.
- When the effective `Jc` is zero, individually valid stored `Jmin` and `Jmax` values remain allowed because no junk packets are sent.
- `Jmin = 50` and `Jmax = 1000` remain valid.

Existing merge semantics are unchanged: an API integer override of zero inherits the server default. An effective `Jc = 0` can therefore come from server defaults, not from a per-client zero override.

### Packet padding: `S1`-`S4`

- `S1` must be between 0 and 1132 inclusive.
- `S2` must be between 0 and 1188 inclusive.
- `S3` must be between 0 and 64 inclusive.
- `S4` must be between 0 and 32 inclusive.
- The padded handshake initiation and response packets must not have the same length: `148 + S1 != 92 + S2`, equivalently `S1 + 56 != S2`.

These limits preserve the current generated `S1`/`S2` range and the official client's generated profiles while preventing values that exceed implementation-compatible packet bounds.

### Dynamic headers: `H1`-`H4`

Each non-empty value must use one of these ASCII decimal formats:

- A single unsigned 32-bit value: `1234`.
- An inclusive unsigned 32-bit range: `1234-5678`.

For ranges, the start must not exceed the end. After defaults and overrides are merged, all four effective header ranges must be present and must not overlap. Whitespace, signs, additional separators, trailing data, and control characters are invalid.

### CPS signature packets: `I1`-`I5`

An empty value remains valid and inherits the server default under the existing merge rules. A non-empty value must consist entirely of one or more supported tags with no text between tags:

- `<b 0xHEX>` for non-empty static bytes. `HEX` must contain an even number of valid hexadecimal digits.
- `<t>` for a four-byte Unix timestamp.
- `<r N>` for random bytes.
- `<rc N>` for random ASCII letters.
- `<rd N>` for random decimal digits.

For `r`, `rc`, and `rd`, `N` must be between 0 and 1000 inclusive. The expanded size of each individual `I` packet must not exceed 1280 bytes. Unknown tags, including `<c>`, malformed arguments, CR/LF, control characters, and any non-tag text are invalid.

The portable tag set is intentionally narrower than the union of all implementation-specific parsers. In particular, the kernel module understands `<c>`, but current `amneziawg-go` does not expose it as a supported CPS tag.

## Architecture

Validation remains owned by `internal/awg`, alongside `AWGParams`, CLI serialization, and client configuration serialization.

Two validation stages are required:

1. Override validation checks explicitly supplied API values. It catches negative integers and malformed non-empty strings before existing merge logic can silently replace them with defaults.
2. Effective-profile validation checks cross-field rules after the client manager merges overrides with defaults. It catches relationships involving inherited values, such as an overridden `Jmin` exceeding the default `Jmax` or an overridden `H1` overlapping a default header range.

Validation errors use an exported `awg.ValidationError` carrying a field name and a safe reason. It unwraps to a stable `awg.ErrInvalidParams` sentinel so HTTP code can classify the error with `errors.Is`. The error never echoes an entire CPS value or any secret material.

## Data Flow

### Create and update requests

1. The HTTP handler decodes the request.
2. The handler validates the supplied overrides and returns HTTP 400 immediately on failure.
3. The manager merges overrides over its defaults.
4. The manager validates the effective profile before generating keys, allocating an IP, changing persisted state, or migrating a peer.
5. Only a valid profile is passed to the interface pool and configuration renderer.

`CreateClient` and `UpdateClient` retain manager-level validation even though handlers validate requests, so non-HTTP callers cannot bypass domain constraints. The restoration path remains separate as described below.

### Startup defaults

After `main` constructs the default `AWGParams` from environment settings and persisted generated parameters, it validates the effective default profile before creating the pool or restoring clients. Invalid operator settings fail startup with a clear contextual error.

### Persisted clients

This change does not add a new strict validation gate for existing persisted client overrides during restoration. Existing restoration and `awg` error handling remain unchanged so an upgrade cannot silently remove or newly disable a previously stored client solely because validation policy was introduced.

## HTTP Error Handling

- Invalid create or update input returns HTTP 400.
- Error responses use the existing JSON error shape.
- Errors from `awg`, device management, storage, and other internal operations retain their existing status mapping.
- Validation must complete before operations that can mutate interface, peer, allocation, or persisted state.

## Documentation Changes

- Document all accepted CPS formats, ranges, cross-field rules, inheritance behavior, and HTTP 400 responses in `docs/api.md`.
- Document default-profile validation and environment constraints in `docs/configuration.md`.
- Correct README examples that currently use `Jmax = 2000` or the non-portable `<c>` CPS tag.
- Update `.ai/rules/architecture.md` and `.ai/rules/security.md` so future changes preserve the new contract.

## Verification

No new test files will be added. The implementation will be verified with:

- `gofmt -w` on changed Go files.
- `go test ./...` as the repository-wide package compilation check.
- `go vet ./...`.
- `go build -o awg-server .`.
- `git diff --check`.
- Manual review of the final diff for generated artifacts, status-code drift, and documentation consistency.
