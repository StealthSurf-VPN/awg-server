# Security Rules

## Authentication

- All API endpoints require `Authorization: Bearer <token>`
- Token compared with constant-time-safe string comparison is NOT used — acceptable for internal service
- Token is set via `AWG_API_TOKEN` env var, never hardcoded

## Key Management

- Server private key generated once and persisted in `/data/clients.json`
- AWG obfuscation params (H1-H4, S1, S2) generated once via `crypto/rand` and persisted in `/data/clients.json`
- All AWG interfaces share the same server private key
- Client private keys stored in JSON for config regeneration
- Each new client receives an independent 32-byte PSK generated with `crypto/rand`; it is stored in JSON and passed to `awg` through stdin, never command-line arguments
- PSKs are not accepted from API callers and are omitted from list, create, and update JSON responses; they appear only in authenticated generated client configurations
- Legacy client records without PSKs remain supported and are not upgraded automatically
- WireGuard keys: Curve25519 with proper clamping
- JSON file permissions: `0600`

## Network Security

- Service listens on all interfaces by default
- HTTP API port (7777) should only be accessible from internal network
- Only the WireGuard UDP ports should be public (base port through base + number of active interfaces)
- Use firewall rules to restrict access to the HTTP API

## Input Validation

- Client ID (`id` in POST body) validated for emptiness and length (max 256 chars)
- Duplicate client IDs rejected (409 Conflict)
- CIDR address validated at config load
- Bearer token checked before any handler execution (`/health` excluded)
- Internal server errors (500) return generic message, details logged server-side only
- `awg_params` deserialized from JSON with Go's type safety
- Per-client MTU accepts 0 for inheritance or values in the inclusive range 1280-1420
- Per-client DNS accepts an empty string for `AWG_DNS` inheritance or one IPv4 address; URLs, hostnames, CIDRs, IPv6 addresses, and lists are rejected
- Per-client persistent keepalive accepts 0 to disable or values through 65535; omission inherits 25
- Per-client port accepts 0 for automatic assignment or values in the inclusive range 1024-65535; uniqueness is enforced (409 Conflict if in use)
- Per-client listen port accepts 0 for automatic client-side selection or values in the inclusive range 1024-65535; it does not reserve or expose a server-side port
- CPS integers are bounded before merge: Jc 0-128, Jmin/Jmax 0-1280, S1 0-1132, S2 0-1188, S3 0-64, and S4 0-32
- When effective Jc is positive, Jmin and Jmax must both be positive and Jmin must be less than Jmax; effective S2 must not equal S1 + 56
- H1-H4 accept only unsigned decimal `uint32` values or inclusive `start-end` ranges; all effective ranges must be present and non-overlapping
- I1-I5 accept only exact `b`, `t`, `r`, `rc`, and `rd` CPS tag sequences; dynamic sizes are 0-1000 and each expanded packet is limited to 1280 bytes
- Invalid AWG overrides and effective profiles return 400 before key generation, IP allocation, peer migration, or persistence
- Interface limit enforced via `AWG_MAX_INTERFACES` (503 when exceeded)
