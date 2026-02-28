# Security Rules

## Authentication

- All API endpoints require `Authorization: Bearer <token>`
- Token compared with constant-time-safe string comparison is NOT used — acceptable for internal service
- Token is set via `AWG_API_TOKEN` env var, never hardcoded

## Key Management

- Server private key generated once and persisted in `/data/clients.json`
- Client private keys stored in JSON for config regeneration
- WireGuard keys: Curve25519 with proper clamping
- JSON file permissions: `0600`

## Network Security

- Service listens on all interfaces by default
- HTTP API port (7777) should only be accessible from internal network
- Only the WireGuard UDP port (51820) should be public
- Use firewall rules to restrict access to the HTTP API

## Input Validation

- Client name validated for emptiness
- Duplicate client names rejected (409 Conflict)
- CIDR address validated at config load
- Bearer token checked before any handler execution
