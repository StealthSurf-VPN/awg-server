# Security Rules

## Authentication

- All `/api` endpoints require `Authorization: Bearer <token>`; `/health` is intentionally unauthenticated
- Token compared with constant-time-safe string comparison is NOT used — acceptable for internal service
- Token is set via `AWG_API_TOKEN` env var, never hardcoded

## Key Management

- Server private key generated once and persisted in
  `{AWG_DATA_DIR}/clients.json`
- Legacy 2.0 generated H1-H4/S1-S2 and private 3.1 generated H1-H4/S1-S4 are
  distinct persisted state. 3.1 generated H values are fixed rather than
  ranges; every effective 3.1 S value is at least 12.
- All AWG interfaces share the same server private key
- Client private keys stored in JSON for config regeneration
- Persisted client private keys are decoded and verified against their stored public keys before any client is restored
- Each new client receives an independent 32-byte PSK generated with `crypto/rand`; it is stored in JSON and passed to `awg` through stdin, never command-line arguments
- PSKs are not accepted from API callers and are omitted from every public
  client response (list, create, update, regenerate, and LAN group); they
  appear only in authenticated generated client configurations.
- Legacy client records without PSKs remain supported and are not upgraded automatically
- `HeaderProtectionKey` is a private, non-zero 32-byte 3.1 secret stored under
  `awg_31.header_keys` behind a random opaque reference. It has no ordinary
  public API field and must never appear in logs, errors, argv, profile-key
  text, source examples, or realistic fixtures. Only controlled storage,
  authenticated client-configuration rendering, and secret server `setconf`
  stdin rendering may encode it as base64.
- Configure server interfaces with `awg setconf <interface> /dev/stdin`; pass
  server private/header keys only on stdin. Do not reintroduce key-bearing
  command arguments or temporary files.
- A persisted 3.1 client with a missing/invalid key reference or key fails
  startup; never silently generate a replacement or fall back to 2.0.
- WireGuard keys: Curve25519 with proper clamping
- JSON file permissions: `0600`

## Network Security

- Service listens on all interfaces by default
- HTTP API port (7777) should only be accessible from internal network
- Only the automatic AWG UDP range and explicitly configured per-client interface ports should be public
- Use firewall rules to restrict access to the HTTP API
- `AWG-LAN` is rule 1 for forwarded VPN-subnet traffic between `awg+` interfaces. It accepts only persisted same-group source/destination pairs and otherwise drops; membership mutation first replaces it with DROP-only rules and never fails open.
- The installer sends its authenticated `/api/clients` gate through a root-only
  curl config file, not a bearer-token command argument. Reject CR/LF in
  environment/auth values before writing either file.

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
- API protocol input accepts only `2`, `2.0`, or `3.1`; `2` normalizes at the
  API boundary. Disk accepts only exact lower-case `protocol_version` with
  canonical `2.0`/`3.1`; a missing disk version is legacy 2.0 and invalid
  stored values fail restore.
- Per-client MTU accepts 0 for inheritance or values in the inclusive range 1280-1420
- Legacy per-client `dns` accepts an empty string for `AWG_DNS` inheritance or one IPv4 address. Mode-based DNS accepts `default`, `system`, or `custom`; custom mode requires one or more plain IPv4 values in `dns_servers`. URLs, hostnames, CIDRs, IPv6 addresses, and mixed legacy/mode fields are rejected.
- 2.0 per-client persistent keepalive accepts a scalar 0 to disable or through
  65535; omission inherits 25. 3.1 accepts only the strict unsigned-16
  scalar/range grammar plus `off` as an input alias for numeric `0`. Canonical
  output and persistence never retain `off` for an unsigned range.
- The 3.1-only range fields (content padding and rekey/timeout controls) use
  the same strict grammar; `random_trailers` and `disable_cookies` are exactly
  `on` or `off`. Every 3.1-only value is rejected for 2.0 before mutation.
- Per-client port accepts 0 for automatic assignment or values in the inclusive range 1024-65535. A new peer can share an existing profile with port 0 or its actual port; a different explicit port is rejected with 409 before peer mutation. PATCH rejects any stored port change while the profile has multiple peers.
- Per-client listen port accepts 0 for automatic client-side selection or values in the inclusive range 1024-65535; it does not reserve or expose a server-side port
- CPS integers are bounded before merge: Jc 0-128, Jmin/Jmax 0-1280, S1 0-1132, S2 0-1188, S3 0-64, and S4 0-32
- When effective Jc is positive, Jmin and Jmax must both be positive and Jmin must be less than Jmax; effective S2 must not equal S1 + 56
- H1-H4 accept only unsigned decimal `uint32` values or inclusive `start-end` ranges; all effective ranges must be present and non-overlapping
- I1-I5 accept only exact `b`, `t`, `r`, `rc`, and `rd` CPS tag sequences; dynamic sizes are 0-1000 and each expanded packet is limited to 1280 bytes
- Invalid AWG overrides and effective profiles return 400 before key generation, IP allocation, peer migration, or persistence
- Split and bypass routing accept only IPv4 CIDRs, mask host bits, stably deduplicate normalized prefixes, and reject invalid syntax or an empty computed result before mutation. Split mode can subtract `excluded_ips` from `allowed_ips`; bypass subtracts exclusions from all IPv4 routes and retains `::/0`. Private, overlapping, and default networks are intentionally accepted when chosen by authenticated callers.
- Interface limit enforced via `AWG_MAX_INTERFACES` (503 when exceeded)
- Runtime qualification requires the minimum installed AWG tools/DKMS packages,
  strict tools output, and a collision-safe temporary 3.1 create/setconf/
  readback/delete probe. Normal startup and the installer fail closed before
  client-owned interface or HTTP work if it fails.
