# Architecture Rules

## Module Boundaries

```text
main.go
  → internal/config    (no dependencies on other internal packages)
  → internal/awg       (depends on config)
  → internal/clients   (depends on awg, config)
  → internal/api       (depends on clients, config)
```

Dependency flow is one-directional. Never import `api` from `clients` or `awg`.

## Device Management

- AmneziaWG 2.0 device is managed via `awg` CLI (kernel module on host)
- Interface created with `ip link add awg0 type amneziawg`
- Device configured via `awg set` with private key passed through stdin
- Obfuscation params: Jc/Jmin/Jmax, S1-S4, H1-H4, I1-I5 (CPS)
- Peer operations via `awg set ... peer` and `awg show ... dump`
- Network configuration (IP, routing, NAT) via `exec.Command`

## Persistence

- Single JSON file at `{AWG_DATA_DIR}/clients.json`
- Atomic writes: write to `.tmp`, then `os.Rename`
- Server private key persisted alongside clients
- On startup: load JSON → re-add all peers to device

## HTTP API

- Standard `net/http` ServeMux (Go 1.22+ method routing)
- Bearer token middleware on all routes
- JSON responses for structured data, plain text for .conf files
- Status codes: 200 (list/get), 201 (create), 204 (delete), 401 (auth), 404 (not found), 409 (conflict)

## Deployment

- Static binary (`CGO_ENABLED=0`), deployed directly to VPN servers
- Requires: `amneziawg` kernel module, `awg` CLI, `iptables`, `iproute2`
- Runs as root or with `NET_ADMIN` capability
- `net.ipv4.ip_forward=1` sysctl required
- Volume at `/data` for persistence
