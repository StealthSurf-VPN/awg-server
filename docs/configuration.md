# Configuration Reference

All configuration is supplied through environment variables. New 3.1 settings
are parsed strictly: malformed values make startup fail instead of silently
falling back. The older integer settings retain their existing fallback parsing
behavior.

## Required settings

| Variable | Description |
| --- | --- |
| `AWG_API_TOKEN` | Bearer token for protected API routes. |
| `AWG_ADDRESS` | Server IPv4 CIDR, such as `10.0.0.1/24`. |
| `AWG_ENDPOINT` | Public address written to generated client configurations. |

## General and 2.0 defaults

| Variable | Default | Description |
| --- | --- | --- |
| `AWG_LISTEN_PORT` | `51820` | Base for auto-assigned interface UDP ports. |
| `AWG_HTTP_PORT` | `7777` | HTTP listen port. |
| `AWG_MTU` | `1420` | Default 2.0 client MTU. |
| `AWG_DNS` | `1.1.1.1` | Default generated-client DNS. |
| `AWG_DATA_DIR` | `/data` | Directory for `clients.json` and `usage.json`. |
| `AWG_INTERFACE` | auto-detect | Outbound NAT interface override. |
| `AWG_JC` | `5` | Legacy/default junk packet count. |
| `AWG_JMIN` | `50` | Legacy/default junk minimum. |
| `AWG_JMAX` | `1000` | Legacy/default junk maximum. |
| `AWG_S3` | `0` | Legacy/default underload padding. |
| `AWG_S4` | `0` | Legacy/default transport padding. |
| `AWG_I1`–`AWG_I5` | empty | Client-only CPS signature packets. |
| `AWG_MAX_INTERFACES` | `0` | Maximum interfaces; zero is unlimited. |

The existing top-level `generated_params` stores 2.0 H1-H4 and S1/S2 defaults.
It remains a 2.0 value and is not reinterpreted as 3.1 state.

## AmneziaWG 3.1 defaults

| Variable | Default | Strict accepted form |
| --- | --- | --- |
| `AWG_DEFAULT_PROTOCOL_VERSION` | `3.1` | `2`, `2.0`, or `3.1`; `2` normalizes to `2.0`. |
| `AWG31_MTU` | `1280` | Decimal integer 1280–1420. |
| `AWG31_PERSISTENT_KEEPALIVE` | `25-35` | Unsigned 16-bit scalar or range; `off` is an input alias for `0`. |
| `AWG31_CONTENT_PADDING_ADDITION` | `10-100` | Unsigned 16-bit scalar or range; `off` is an input alias for `0`. |
| `AWG31_REKEY_AFTER_TIME` | `100-120` | Unsigned 16-bit scalar or range; `off` is an input alias for `0`. |
| `AWG31_REKEY_TIMEOUT` | `3-7` | Unsigned 16-bit scalar or range; `off` is an input alias for `0`. |
| `AWG31_REJECT_AFTER_TIME` | `150-180` | Unsigned 16-bit scalar or range; `off` is an input alias for `0`. |
| `AWG31_KEEPALIVE_TIMEOUT` | `5-15` | Unsigned 16-bit scalar or range; `off` is an input alias for `0`. |
| `AWG31_MAX_HANDSHAKE_ATTEMPTS` | `15-20` | Unsigned 16-bit scalar or range; `off` is an input alias for `0`. |
| `AWG31_RANDOM_TRAILERS` | `on` | Exactly `on` or `off`. |
| `AWG31_DISABLE_COOKIES` | `off` | Exactly `on` or `off`. |

The unsigned-range grammar is ASCII decimal `N` or `N-M`; the compatibility
input alias `off` means `0`. It rejects whitespace, signed values, floats,
exponent notation, empty values, reversed ranges, and overflow. API JSON
accepts a scalar as a number or string, while a range or `off` is a string.
Canonical API and persisted output uses numeric `0` for `off`, numeric `N` for
an equal range `N-N`, and a string for every other range. The same canonical
values are passed to AWG tools and used for interface profile identity.

`AWG_DEFAULT_PROTOCOL_VERSION` affects only new `POST /api/clients` requests
and an omitted `protocol_version` generator query. It never changes an existing
client. Setting it to `2.0` is an operator-controlled phased-rollout fallback.

3.1 defaults combine the environment values with persisted generated H1-H4 and
S1-S4. Fresh 3.1 generation uses four fixed unique H values, S1/S2 in 15–150,
S3 in 15–63, and S4 equal to 12. Every effective 3.1 S value must be at least
12. Fixed generated H values avoid the current target-runtime ranged-header
throughput/MAC1 risk; a caller may still submit a valid explicit H range.
`DisableCookies=off` intentionally retains cookie replies for denial-of-service
resistance.

## Version-aware client overrides

`awg_params` is validated after its target version is resolved and before any
key, address, interface, or persistence mutation.

- For 2.0, `persistent_keepalive` is only a scalar integer from 0 through
  65535. All 3.1-only fields are rejected.
- For 3.1, `persistent_keepalive` and the six timing/padding fields use the
  unsigned-range grammar above. `random_trailers` and `disable_cookies` are
  `on` or `off`.
- The 3.1-only fields are `content_padding_addition`, `rekey_after_time`,
  `rekey_timeout`, `reject_after_time`, `keepalive_timeout`,
  `max_handshake_attempts`, `random_trailers`, and `disable_cookies`.
- `port` is kept separate for interface allocation. Client listen port, MTU,
  DNS, persistent keepalive, I1-I5, routing, and peer PSKs are client-side or
  per-peer state and do not participate in interface identity.

`PATCH` replaces the supplied `awg_params` object rather than merging its
stored fields. JSON `null` resets it to the target version's defaults. A
version-only migration preserves common overrides but rejects an incompatible
one rather than discarding it; send `awg_params:null` or a corrected object to
make that change explicit.

## Persistence and secret boundaries

`{AWG_DATA_DIR}/clients.json` is atomically replaced through a mode-`0600`
temporary file and rename. It does not call file or directory `fsync`, so a
successful Save is the logical commit boundary, not an absolute durability
guarantee across a host crash.

The 3.1 state is a private top-level section. This is a shape-only example;
the placeholders are not usable key material:

```json
{
  "awg_31": {
    "default_header_key_id": "<opaque-id>",
    "generated_params": {
      "h1": "<fixed-decimal>",
      "h2": "<fixed-decimal>",
      "h3": "<fixed-decimal>",
      "h4": "<fixed-decimal>",
      "s1": 15,
      "s2": 16,
      "s3": 15,
      "s4": 12
    },
    "header_keys": {
      "<opaque-id>": {
        "header_protection_key": "<base64-encoded-32-byte-value>"
      }
    }
  },
  "clients": [
    {
      "id": "client-example",
      "protocol_version": "3.1",
      "header_key_id": "<opaque-id>"
    }
  ]
}
```

The example is not a complete valid file. A non-empty `clients` list still
requires the existing top-level `server_private_key` and legacy
`generated_params` state, even when all listed clients are 3.1. Missing those
values fails startup; the service does not generate replacement state for an
existing client set.

`header_key_id` and the header-protection key never appear in list/create/update
responses, logs, profile identifiers, or command-line arguments. The key is
made available only to the authenticated generated configuration that requires
it. It is a 32-byte non-zero value; key IDs are random opaque references and
are not derived from a key.

On startup, the server decodes all state first. If there is no 3.1 state and no
3.1 client, it creates a pending default generated profile/key in memory and
saves it only after the entire restore succeeds. If a persisted 3.1 client is
missing or has an invalid key reference, key, generated parameters, or profile,
startup fails closed; it never invents replacement state or falls back to 2.0.

The restore plan validates all clients, profiles, ports, and interface limits
before it changes a client-owned device. After a successful restore it writes
one normalization save for pending state, explicit legacy versions, and other
canonical values, including persisted `off` and equal-range aliases. A failed
normalization save aborts API startup and closes the pool best-effort. A
mutation stages garbage collection of unreferenced
non-default 3.1 header keys in prospective state before Save; the new key map
becomes authoritative only after a successful Save. Default and still-referenced
keys remain.

Persisted protocol values are stricter than API input: the field name must be
exact lower-case `protocol_version` and the value must be canonical `"2.0"` or
`"3.1"`. A missing persisted field is legacy 2.0; the API-only alias `"2"`, a
case-variant field, `null`, a number, or an unknown value fails restore.

## Interface identity

An immutable internal profile selects a pooled interface. Its identity includes
the protocol version, H1-H4, S1-S4, Jc/Jmin/Jmax, 3.1 server-applied ranges and
toggles, and (for 3.1) the private header-protection key. It excludes requested
port, client listen port, MTU, DNS, persistent keepalive, I1-I5, routing, and
peer PSK. Clients can share an interface only when this exact effective
server-side profile matches.

The pool uses an explicit `port` only to allocate/listen. A new peer may share
an existing profile with requested port zero or that interface's actual port;
a different explicit port is a conflict. Interfaces are created on demand and
removed when empty, subject to `AWG_MAX_INTERFACES`.

## Usage and transaction boundary

Interface migrations and regeneration take a complete usage snapshot before
mutation. A dump failure, malformed row, or empty active-interface dump aborts
before moving a peer. API mutations coordinate prospective storage and
best-effort device rollback, but kernel, filesystem, firewall, and process
failure cannot be globally atomic. Inspect live state after a generic `500`.

## Runtime qualification

Normal startup performs its AWG 3.1 runtime qualifier only after the pure
storage/restore plan validates and before it creates a pool, firewall state, or
HTTP server. It requires installed Ubuntu 22.04 package versions, strict
`awg --version` output, and a collision-safe temporary 3.1 interface
create/setconf/readback/delete probe. Use `awg-server check-runtime` to run the
same implementation explicitly; it is not a substitute for production
handshake or throughput qualification.
