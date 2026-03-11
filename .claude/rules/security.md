# Security Rules

## Authentication

- All API endpoints require `Authorization: Bearer <token>`
- Token compared with constant-time-safe string comparison is NOT used — acceptable for internal service
- Token is set via `AWG_API_TOKEN` env var, never hardcoded

## Key Management

- Server private key generated once and persisted in `/data/clients.json`
- All AWG interfaces share the same server private key
- Client private keys stored in JSON for config regeneration
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
- Port range validated (1024-65535), uniqueness enforced (409 Conflict if in use)
- Interface limit enforced via `AWG_MAX_INTERFACES` (503 when exceeded)
