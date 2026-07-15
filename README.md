# awg-server

HTTP API server for managing **AmneziaWG 2.0** VPN clients. Uses the **AmneziaWG kernel module** on the host with the `awg` CLI tool — kernel-level performance with DPI obfuscation via CPS (Custom Protocol Signature).

Supports **per-client obfuscation profiles** — each unique set of CPS parameters gets its own AWG interface, created on demand.

Every newly created client also receives a unique, server-generated WireGuard preshared key (PSK). The PSK is installed on the AWG peer and included only in that client's generated configuration.

## Quick Install (Ubuntu 22.04)

This example targets an Ubuntu 22.04 host or VM. It installs AmneziaWG 2.0 from the official Amnezia PPA and one exact signed `awg-server` release. Containers are not supported for production because they share the host kernel and cannot provide an independent AmneziaWG module. AmneziaVPN clients must be version 4.8.12.9 or newer; generate new client configurations instead of reusing AmneziaWG 1.0 configurations.

Before running it, obtain both the intended stable `MAJOR.MINOR.PATCH` server version and the project's Ed25519 release public key through a trusted administrative channel, export that version as `AWG_SERVER_VERSION`, and install the key at `/etc/awg-server/release-signing-public.pem`. Legacy unsigned releases and mutable `latest` aliases are intentionally rejected.

```bash
set -Eeuo pipefail
: "${AWG_SERVER_VERSION:?export the trusted release version as AWG_SERVER_VERSION=MAJOR.MINOR.PATCH}"
[[ $AWG_SERVER_VERSION =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]

# 1. Install the official AmneziaWG 2.0 kernel module and awg CLI
apt-get update
apt-get install -y \
  build-essential ca-certificates curl dkms gnupg2 iproute2 iptables \
  libelf-dev openssl \
  python3-launchpadlib software-properties-common \
  "linux-headers-$(uname -r)"

# XanMod kernels installed by the StealthSurf host bootstrap are built with
# LLVM 19. Install the matching toolchain before DKMS builds AmneziaWG.
if [[ $(uname -r) == *xanmod* ]]; then
  install -d -m 0755 /etc/apt/keyrings
  curl -fsSL https://apt.llvm.org/llvm-snapshot.gpg.key \
    | gpg --batch --yes --dearmor -o /etc/apt/keyrings/llvm-snapshot.gpg
  echo 'deb [signed-by=/etc/apt/keyrings/llvm-snapshot.gpg] https://apt.llvm.org/jammy/ llvm-toolchain-jammy-19 main' \
    > /etc/apt/sources.list.d/llvm-19.list
  apt-get update
  apt-get install -y clang-19 lld-19 llvm-19
  for tool in clang clang++ ld.lld llvm-ar llvm-nm llvm-objcopy llvm-objdump llvm-readelf llvm-strip; do
    update-alternatives --install "/usr/bin/$tool" "$tool" "/usr/bin/$tool-19" 190
    update-alternatives --set "$tool" "/usr/bin/$tool-19"
  done
  export PATH=/usr/lib/llvm-19/bin:$PATH
fi

add-apt-repository -y ppa:amnezia/ppa
apt-get update
apt-get install -y amneziawg
modprobe amneziawg
modinfo amneziawg >/dev/null
awg --version

# 2. Enable IP forwarding
sysctl -w net.ipv4.ip_forward=1
echo "net.ipv4.ip_forward=1" >> /etc/sysctl.conf

# 3. Download and verify the exact signed awg-server release
case "$(uname -m)" in
  x86_64) ASSET=awg-server-linux-amd64 ;;
  aarch64|arm64) ASSET=awg-server-linux-arm64 ;;
  *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac
RELEASE_DIR=$(mktemp -d)
trap 'rm -rf "$RELEASE_DIR"' EXIT
RELEASE_URL="https://github.com/StealthSurf-VPN/awg-server/releases/download/v$AWG_SERVER_VERSION"
curl -fL "$RELEASE_URL/$ASSET" -o "$RELEASE_DIR/$ASSET"
curl -fL "$RELEASE_URL/SHA256SUMS" -o "$RELEASE_DIR/SHA256SUMS"
curl -fL "$RELEASE_URL/SHA256SUMS.sig" -o "$RELEASE_DIR/SHA256SUMS.sig"
KEY_DESCRIPTION=$(openssl pkey -pubin -in /etc/awg-server/release-signing-public.pem -text -noout)
KEY_TYPE=${KEY_DESCRIPTION%%$'\n'*}
unset KEY_DESCRIPTION
test "$KEY_TYPE" = 'ED25519 Public-Key:'
openssl pkeyutl -verify -rawin -pubin \
  -inkey /etc/awg-server/release-signing-public.pem \
  -sigfile "$RELEASE_DIR/SHA256SUMS.sig" \
  -in "$RELEASE_DIR/SHA256SUMS"
CHECKSUM_LINE=$(grep -E "^[0-9a-f]{64}  ${ASSET}$" "$RELEASE_DIR/SHA256SUMS")
test "$(printf '%s\n' "$CHECKSUM_LINE" | grep -c .)" -eq 1
(cd "$RELEASE_DIR" && printf '%s\n' "$CHECKSUM_LINE" | sha256sum --check --strict -)
chmod 0755 "$RELEASE_DIR/$ASSET"
test "$("$RELEASE_DIR/$ASSET" version)" = "awg-server $AWG_SERVER_VERSION"
install -o root -g root -m 0755 "$RELEASE_DIR/$ASSET" /usr/local/bin/awg-server
rm -rf "$RELEASE_DIR"
trap - EXIT

# 4. Create data directory
mkdir -p /data

# 5. Create systemd service
cat > /etc/systemd/system/awg-server.service <<EOF
[Unit]
Description=AmneziaWG Server
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
ExecStartPre=/sbin/modprobe amneziawg
ExecStart=/usr/local/bin/awg-server
Restart=always
RestartSec=5

Environment=AWG_API_TOKEN=your-secret-token
Environment=AWG_ADDRESS=10.0.0.1/24
Environment=AWG_ENDPOINT=your.server.ip

# Optional application defaults:
# Environment=AWG_JC=5
# Environment=AWG_JMIN=50
# Environment=AWG_JMAX=1000
# Environment=AWG_S3=0
# Environment=AWG_S4=0
# Environment=AWG_LISTEN_PORT=51820
# Environment=AWG_HTTP_PORT=7777
# Environment=AWG_DNS=1.1.1.1
# Environment=AWG_MTU=1420
# Environment=AWG_MAX_INTERFACES=0

[Install]
WantedBy=multi-user.target
EOF

# 6. Start and enable on boot
systemctl daemon-reload
systemctl enable --now awg-server
```

Check status:

```bash
systemctl status awg-server
journalctl -u awg-server -f
```

## Prerequisites

- Ubuntu 22.04 on a host or VM, not inside Docker/LXC
- [`amneziawg`](https://launchpad.net/~amnezia/+archive/ubuntu/ppa) installed from the official Amnezia PPA; it provides both the DKMS module and `awg` CLI
- AmneziaVPN 4.8.12.9 or newer, or another AmneziaWG 2.0-compatible client
- `iptables`, `iproute2` (usually already present)
- `net.ipv4.ip_forward=1` sysctl enabled

## CLI Commands

```bash
# Check current version
awg-server version

# Cryptographically verified self-update to the latest stable GitHub release
awg-server update

# Start the server (default, no arguments)
awg-server
```

On Linux and macOS, `awg-server update` is enabled only in official release binaries that embed the Ed25519 release public key. It rejects non-stable versions, downgrades, concurrent updates, unexpected assets or URLs, invalid signatures, non-canonical manifests, checksum mismatches, and a binary whose embedded version does not match the release. It locks and rechecks the actual installed binary before any release asset download and immediately before replacement. A source build without `RELEASE_PUBLIC_KEY` fails closed before network access. Windows self-update also fails before network access because a running `.exe` cannot be replaced atomically; install a separately verified signed Windows asset instead. After a successful in-place update, restart the service: `systemctl restart awg-server`.

## Build

```bash
# Build for current platform (with version)
make build VERSION=1.0.0
# -> ./awg-server

# Build for all platforms (linux, darwin, windows × amd64, arm64)
make build-all VERSION=1.0.0
# -> dist/awg-server-<os>-<arch>[.exe]

# Static analysis
make vet

# Remove dist/ release artifacts (does not remove ./awg-server)
make clean
```

Requires Go 1.24+. `make build` writes the current-platform binary to `./awg-server`; `make build-all` recreates `dist/` and writes all six release targets there. The automated release workflow supplies the canonical base64-encoded Ed25519 public PEM through `RELEASE_PUBLIC_KEY`; ordinary source builds intentionally omit updater trust.

## Tests and Automation

```bash
# Full deterministic API and package suite
go test -race -count=1 ./...

# Release marker contract
bash scripts/release-marker_test.sh

# Release notes contract
bash scripts/release-notes_test.sh

# Previous stable release selection
bash scripts/release-previous-tag_test.sh

# Required static checks
go vet ./...
go build -trimpath -o /tmp/awg-server .
```

The API suite runs the real router, handlers, manager, temporary JSON storage, routing/DNS/configuration logic, key generation, and usage collector while replacing only host-level AWG device operations. It covers every registered HTTP operation and does not require root, an AWG kernel module, or external network access.

GitHub Actions runs these checks for pull requests and `main`. A strict `release:vMAJOR.MINOR.PATCH` marker in the immutable commit message that lands on `main` starts publication of all six platform binaries, checksums, and an Ed25519 signature after CI passes and the protected `release-signing` Environment is approved. The release description contains the matching changelog section followed by a clickable `Full Changelog` range from the previous stable release. Build scripts never receive the private signing key: a separate job without a source checkout or `GITHUB_TOKEN` permissions validates the unsigned artifact, requires an exact Ed25519 keypair match, and signs only the checksum manifest. The workflow publishes GitHub Releases only and never deploys binaries to servers. See [CI and release automation](docs/ci-cd.md) for the exact marker contract, release-note format, signing-key setup, release gates, GitHub settings, and manual fallback.

## Deploy

Copy `awg-server` binary to the VPN server and run:

```bash
AWG_API_TOKEN=your-secret-token \
AWG_ADDRESS=10.0.0.1/24 \
AWG_ENDPOINT=your.server.ip \
./awg-server
```

## API

All `/api` endpoints require `Authorization: Bearer <AWG_API_TOKEN>`.

| Method | Path | Success |
| ------ | ---- | ------- |
| `GET` | `/health` | `200` (no authentication) |
| `GET` | `/api/clients` | `200` |
| `POST` | `/api/clients` | `201` |
| `PATCH` | `/api/clients/{id}` | `200` |
| `GET` | `/api/clients/{id}/configuration` | `200` |
| `GET` | `/api/clients/{id}/stats` | `200` |
| `DELETE` | `/api/clients/{id}` | `204` |
| `POST` | `/api/awg-params/generate` | `200` |
| `POST` | `/api/clients/{id}/regenerate-awg-params` | `200` |

There is no single-client `GET /api/clients/{id}` route. Create and PATCH accept exactly one JSON value, limit the body to 1 MiB, and ignore unknown JSON fields; PATCH objects replace the supplied top-level field rather than merging it, while JSON `null` resets that field. Other handlers do not read request bodies. Wrong methods and unknown paths are handled by Go's `net/http` mux as `405` and `404` respectively. See the [API reference](docs/api.md#http-routing-authentication-and-bodies) for the complete status and error contract.

```bash
# Health check (no auth)
curl http://localhost:7777/health

# List clients
curl http://localhost:7777/api/clients -H "Authorization: Bearer $TOKEN"

# Create client (default obfuscation params from env)
curl -X POST http://localhost:7777/api/clients \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"id":"my-client-uuid"}'

# Create client with custom server port, client listen port, MTU, DNS, persistent keepalive, and obfuscation params
curl -X POST http://localhost:7777/api/clients \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"id":"my-client-uuid","awg_params":{"port":51825,"client_listen_port":54321,"mtu":1280,"dns":"9.9.9.9","persistent_keepalive":60,"jc":5,"jmin":50,"jmax":1000,"s1":40,"s2":80,"s3":20,"h1":"100000-800000"}}'

# Update client listen port, MTU, DNS, persistent keepalive, and obfuscation params
curl -X PATCH http://localhost:7777/api/clients/my-client-uuid \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"awg_params":{"port":51825,"client_listen_port":54321,"mtu":1280,"dns":"9.9.9.9","persistent_keepalive":0,"jc":10,"jmin":100,"jmax":1000}}'

# Update client split routing policy
curl -X PATCH http://localhost:7777/api/clients/my-client-uuid \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"routing":{"mode":"split","allowed_ips":["10.0.0.0/8","172.16.0.0/12"]}}'

# Create a client with custom DNS servers
curl -X POST http://localhost:7777/api/clients \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"id":"demo-custom-dns","awg_params":{"dns_mode":"custom","dns_servers":["1.1.1.1","1.0.0.1"]}}'

# Create a client that keeps the device's system DNS resolver
curl -X POST http://localhost:7777/api/clients \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"id":"demo-system-dns","awg_params":{"dns_mode":"system"}}'

# Create a bypass client that routes everything except the excluded IPv4 CIDRs
curl -X POST http://localhost:7777/api/clients \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"id":"demo-bypass","routing":{"mode":"bypass","excluded_ips":["10.0.0.0/8","192.168.0.0/16"]}}'

# Create a split client and subtract exclusions from its included IPv4 set
curl -X POST http://localhost:7777/api/clients \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"id":"demo-split","routing":{"mode":"split","allowed_ips":["10.0.0.0/8"],"excluded_ips":["10.20.0.0/16"]}}'

# Generate a standalone H1-H4/S1-S2 fragment without changing server state
curl -X POST http://localhost:7777/api/awg-params/generate \
  -H "Authorization: Bearer $TOKEN"

# Regenerate and apply H1-H4/S1-S2 for one client (no request body)
curl -X POST http://localhost:7777/api/clients/demo-custom-dns/regenerate-awg-params \
  -H "Authorization: Bearer $TOKEN"

# Get client config (.conf)
curl http://localhost:7777/api/clients/my-client-uuid/configuration \
  -H "Authorization: Bearer $TOKEN"

# Get client usage stats (accumulated rx/tx, last handshake)
curl http://localhost:7777/api/clients/my-client-uuid/stats \
  -H "Authorization: Bearer $TOKEN"
# → {"rx_bytes":1073741824,"tx_bytes":5368709120,"last_handshake":"2026-04-01T12:00:00Z"}

# Delete client
curl -X DELETE http://localhost:7777/api/clients/my-client-uuid \
  -H "Authorization: Bearer $TOKEN"
```

Routing mode `full` preserves full tunnel, `bypass` subtracts `excluded_ips` from all IPv4 routes, and `split` can subtract exclusions from its `allowed_ips`. Routing and DNS are client-only and do not change interface grouping or the server-side peer `/32`. After a routing, DNS, or per-client H/S regeneration change, fetch the generated configuration and reapply it on the client device. See the [API reference](docs/api.md) for validation, deterministic route subtraction, the 4,096/16,384 routing limits, error statuses, and the regeneration reapplication warning.

Create, update, regeneration, and delete return a generic `500` when device work or `clients.json` persistence fails. The manager commits its in-memory client map only after persistence succeeds and attempts to restore device state when a later step fails. Rollback can itself fail, so a `500` never guarantees that live kernel state is pristine; inspect the server logs and AWG interfaces after device or storage failures. Startup is fail-fast: when persisted clients exist, both the top-level `server_private_key` and `generated_params` must exist; invalid or mismatched client keys, invalid persisted settings, or any client that cannot be restored also prevent the HTTP server from starting. Interfaces created before a failed restoration are cleaned up best-effort, and an HTTP bind failure terminates the process non-zero after cleanup.

Usage is collected on startup and every 60 seconds and persisted in `{AWG_DATA_DIR}/usage.json`; shutdown performs a final collect/save. Every interface-level PATCH and per-client H/S regeneration takes a required complete snapshot before migration so counters from the old interface are accumulated first. A command error, malformed peer row, or active interface returning no peers aborts the update before mutation. The snapshot updates memory immediately but reaches `usage.json` on the next scheduled or shutdown save. Invalid persisted usage data is logged and replaced in memory with an empty stats map rather than crashing the collector.

## Configuration

Environment variables:

| Variable | Required | Default | Description |
| -------- | -------- | ------- | ----------- |
| `AWG_API_TOKEN` | yes | — | Bearer token for API auth |
| `AWG_ADDRESS` | yes | — | Server VPN address (CIDR), e.g. `10.0.0.1/24` |
| `AWG_ENDPOINT` | yes | — | Public IP/hostname for client configs |
| `AWG_LISTEN_PORT` | no | `51820` | Base WireGuard UDP port (auto-assigned sequentially; per-client `port` accepts 1024-65535, while omitted or zero uses automatic assignment). A new client can join a profile with port 0 or its actual port; PATCH rejects any stored port change while that profile is shared. |
| `AWG_HTTP_PORT` | no | `7777` | HTTP API port |
| `AWG_MTU` | no | `1420` | Default MTU for client configs (per-client override: `mtu` in `awg_params`, range 1280-1420) |
| `AWG_DNS` | no | `1.1.1.1` | Default DNS inherited by omitted/default per-client settings; custom mode uses `dns_servers`, while system mode omits the client `DNS` line |
| `AWG_DATA_DIR` | no | `/data` | Persistence directory |
| `AWG_INTERFACE` | no | auto-detect | Override outbound network interface for NAT |
| `AWG_MAX_INTERFACES` | no | `0` | Max AWG interfaces (0 = unlimited) |

### Auto-Generated Parameters

On first start, the server generates and persists unique obfuscation values in `/data/clients.json`:

| Parameter | Generation | Purpose |
| --------- | ---------- | ------- |
| **H1-H4** | Random non-overlapping ranges (format `min-max`) | Replace WireGuard message type headers. Zero performance impact. |
| **S1** | Random 15-150 | Init handshake packet padding |
| **S2** | Random 15-150, `S1 + 56 ≠ S2` | Response handshake packet padding |

These are reused across restarts. No env vars needed.

### Configurable Parameters

| Variable | Default | What it does | Impact |
| -------- | ------- | ------------ | ------ |
| `AWG_JC` | `5` | Junk packets sent during handshake | **0** = off, **3-8** = good. No effect after connect. |
| `AWG_JMIN` | `50` | Junk packet minimum size (bytes) | Wider range = harder to fingerprint. |
| `AWG_JMAX` | `1000` | Junk packet maximum size (bytes) | **500-1000** = good. |
| `AWG_S3` | `0` | Extra bytes added to cookie reply packets | **0-32** = good. Only under load. |
| `AWG_S4` | `0` | Extra bytes added to **every** data packet | **0** = recommended. Adds overhead to every packet. |
| `AWG_I1`-`AWG_I5` | empty | CPS signature packets (client config only) | Decoy UDP packets mimicking another protocol. Uses [CPS tag format](https://github.com/amnezia-vpn/amneziawg-go#i-parameters). |

Advanced clients can override `persistent_keepalive` through `awg_params`. Omit the field to use 25 seconds, set it to 0 to disable keepalive, or provide an interval from 1 through 65535. The new value takes effect after the generated configuration is downloaded and reapplied on the client device.

Clients can keep the legacy single-IPv4 `awg_params.dns` override or use `dns_mode`: `default` inherits `AWG_DNS`, `custom` renders the unique IPv4 values from `dns_servers`, and `system` omits the complete client `DNS` line. The legacy field cannot be combined with mode-based fields. See the [DNS contract](docs/api.md#dns-settings) for accepted shapes and validation. A DNS change takes effect after the generated configuration is downloaded and reapplied on the client device.

Clients can set a local UDP port with `awg_params.client_listen_port` in the range 1024-65535. Omit it or set it to 0 to let the client choose automatically. This renders `ListenPort` in the client `[Interface]` and does not change the server `Endpoint` port.

### CPS Parameter Validation

Invalid CPS overrides return `400 Bad Request` before client state changes. The merged server/client profile is checked again so cross-field conflicts with inherited defaults are also rejected. Server defaults are validated during startup.

| Parameters | Accepted values |
| ---------- | --------------- |
| `Jc` | 0-128 |
| `Jmin`, `Jmax` | 0-1280; when effective `Jc > 0`, both are positive and `Jmin < Jmax` |
| `S1`, `S2`, `S3`, `S4` | 0-1132, 0-1188, 0-64, 0-32; effective `S2` must not equal `S1 + 56` |
| `H1`-`H4` | Unsigned decimal `uint32` or inclusive `start-end`; all effective ranges are required and non-overlapping |
| `I1`-`I5` | Only `<b 0xHEX>`, `<t>`, `<r N>`, `<rc N>`, and `<rd N>` tags; `N` is 0-1000 and the expanded packet is at most 1280 bytes |

Zero integer overrides and empty string overrides inherit server defaults. `persistent_keepalive` is the exception: an explicit zero disables it.

### Per-Client Preshared Keys

The server generates a unique 32-byte PSK for every new client. It stores the key in `/data/clients.json` with the rest of the client secrets, installs it on the server peer through stdin, and adds `PresharedKey` to the authenticated `.conf` response. The PSK is not accepted in create/update requests and is not exposed by list, create, or update responses.

Persisted clients created by an older server version have no PSK and continue to work unchanged. They are not upgraded automatically because enabling a PSK on only the server would break their existing client configurations.

### Obfuscation Profiles

> **Rule of thumb:** H1-H4 and S1/S2 are auto-generated. `Jc/Jmin/Jmax` only affect connection time. `S4` affects every packet — use with care.

**Default** (no extra env vars needed) — headers + light junk at handshake:

H1-H4, S1, S2 auto-generated. Jc=5, Jmin=50, Jmax=1000 (defaults).

Ping: same as plain WireGuard after connect. Protection: blocks signature + size-based DPI.

**Minimum latency** — disable junk packets:

```bash
AWG_JC=0 AWG_JMIN=0 AWG_JMAX=0
```

Auto-generated H1-H4 still provide header masking (zero overhead).

**Maximum stealth** — for regions with aggressive DPI (China, Iran, Turkmenistan):

```bash
AWG_JC=8 AWG_JMIN=50 AWG_JMAX=1000
AWG_I1='<b 0xc0><r 32><t>'
```

Ping: same after connect (~100ms extra at handshake). Protection: maximum without per-packet overhead.

## Multi-Interface Architecture

AmneziaWG sets CPS obfuscation parameters at the **interface level**, not per-peer. To support per-client custom parameters, the server manages a **pool of interfaces**:

- Each unique set of CPS parameters gets its own `awgN` interface (awg0, awg1, awg2, ...)
- Clients with identical CPS parameters share an interface
- Per-client `mtu`, all legacy and mode-based DNS fields, `persistent_keepalive`, all routing fields, and PSK do not affect interface grouping; PSK is also installed on the corresponding server peer
- Each interface listens on its own UDP port (explicit `port` from `awg_params`, or auto-assigned sequentially from base port); every explicit port must also be open in the host firewall
- A new client can join an existing profile with port 0 or the interface's actual port; a different explicit port is rejected with `409` before peer mutation, and PATCH rejects any stored port change while that profile is shared
- Interfaces are created on demand and removed through coordinated peer/route/interface cleanup when their last peer is deleted; failures return `500` and trigger best-effort restoration
- All interfaces share the same server private key

```text
main.go → config → awg (pool, params, keygen) → clients (manager, storage) → api (server, handlers)
```

- **Kernel module** — `amneziawg-linux-kernel-module` on host, `awg` CLI for management
- **Static binary** — `CGO_ENABLED=0`, no external Go dependencies beyond `golang.org/x/crypto`
- **Persistence** via temporary JSON files and rename, coordinated with device changes; rollback and crash durability are not absolute
- **IP allocation** sequential from .2, freed IPs reusable
- **Auth** Bearer token on all `/api` endpoints; `/health` is unauthenticated
