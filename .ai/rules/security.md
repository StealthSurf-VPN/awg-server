# Security Rules

## Authentication

- All `/api` endpoints require `Authorization: Bearer <token>`; `/health` is intentionally unauthenticated
- Token compared with constant-time-safe string comparison is NOT used — acceptable for internal service
- Token is set via `AWG_API_TOKEN` env var, never hardcoded

## Key Management

- Server private key generated once and persisted in `/data/clients.json`
- AWG obfuscation params (H1-H4, S1, S2) generated once via `crypto/rand` and persisted in `/data/clients.json`
- All AWG interfaces share the same server private key
- Client private keys stored in JSON for config regeneration
- Persisted client private keys are decoded and verified against their stored public keys before any client is restored
- Each new client receives an independent 32-byte PSK generated with `crypto/rand`; it is stored in JSON and passed to `awg` through stdin, never command-line arguments
- PSKs are not accepted from API callers and are omitted from list, create, and update JSON responses; they appear only in authenticated generated client configurations
- Legacy client records without PSKs remain supported and are not upgraded automatically
- WireGuard keys: Curve25519 with proper clamping
- JSON file permissions: `0600`

## Network Security

- Service listens on all interfaces by default
- HTTP API port (7777) should only be accessible from internal network
- Only the automatic AWG UDP range and explicitly configured per-client interface ports should be public
- Use firewall rules to restrict access to the HTTP API
- `AWG-LAN` is rule 1 for forwarded VPN-subnet traffic between `awg+` interfaces. It accepts only persisted same-group source/destination pairs and otherwise drops; membership mutation first replaces it with DROP-only rules and never fails open.

## Input Validation

- Client ID (`id` in POST body) validated for emptiness and length (max 256 chars)
- Duplicate client IDs rejected (409 Conflict)
- LAN-group PATCH requires a non-empty unique `client_ids` list, validates every client under the manager mutex before mutation, and requires a non-empty `lan_group_id`
- CIDR address validated at config load
- Bearer token checked before any handler execution (`/health` excluded)
- Internal server errors (500) return generic message, details logged server-side only
- Create and PATCH accept exactly one JSON value up to 1 MiB; unknown fields are ignored, and all other handlers ignore request bodies
- Firewall input contains only the validated configured IPv4 network and generated or validated persisted client IPv4 addresses; opaque `lan_group_id` values are compared in memory and never interpolated into commands or restore input
- `awg_params` deserialized from JSON with Go's type safety
- Per-client MTU accepts 0 for inheritance or values in the inclusive range 1280-1420
- Legacy per-client `dns` accepts an empty string for `AWG_DNS` inheritance or one IPv4 address. Mode-based DNS accepts `default`, `system`, or `custom`; custom mode requires one or more plain IPv4 values in `dns_servers`. URLs, hostnames, CIDRs, IPv6 addresses, and mixed legacy/mode fields are rejected.
- Per-client persistent keepalive accepts 0 to disable or values through 65535; omission inherits 25
- Per-client port accepts 0 for automatic assignment or values in the inclusive range 1024-65535. A new peer can share an existing profile with port 0 or its actual port; a different explicit port is rejected with 409 before peer mutation. PATCH rejects any stored port change while the profile has multiple peers.
- Per-client listen port accepts 0 for automatic client-side selection or values in the inclusive range 1024-65535; it does not reserve or expose a server-side port
- CPS integers are bounded before merge: Jc 0-128, Jmin/Jmax 0-1280, S1 0-1132, S2 0-1188, S3 0-64, and S4 0-32
- When effective Jc is positive, Jmin and Jmax must both be positive and Jmin must be less than Jmax; effective S2 must not equal S1 + 56
- H1-H4 accept only unsigned decimal `uint32` values or inclusive `start-end` ranges; all effective ranges must be present and non-overlapping
- I1-I5 accept only exact `b`, `t`, `r`, `rc`, and `rd` CPS tag sequences; dynamic sizes are 0-1000 and each expanded packet is limited to 1280 bytes
- Invalid AWG overrides and effective profiles return 400 before key generation, IP allocation, peer migration, or persistence
- Split and bypass routing accept only IPv4 CIDRs, mask host bits, stably deduplicate normalized prefixes, and reject invalid syntax or an empty computed result before mutation. Split mode can subtract `excluded_ips` from `allowed_ips`; bypass subtracts exclusions from all IPv4 routes and retains `::/0`. Private, overlapping, and default networks are intentionally accepted when chosen by authenticated callers.
- Interface limit enforced via `AWG_MAX_INTERFACES` (503 when exceeded)
