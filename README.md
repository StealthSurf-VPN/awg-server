# awg-server

HTTP API server for managing **AmneziaWG 2.0** VPN clients. Uses the **AmneziaWG kernel module** on the host with the `awg` CLI tool — kernel-level performance with DPI obfuscation via CPS (Custom Protocol Signature).

Supports **per-client obfuscation profiles** — each unique set of CPS parameters gets its own AWG interface, created on demand.

## Quick Install (Linux)

One-liner that installs AmneziaWG 2.0, downloads the latest `awg-server` binary, and gets you ready to run:

```bash
# 1. Install AmneziaWG 2.0 kernel module (DKMS, from source)
apt update && apt install -y build-essential git dkms linux-headers-$(uname -r)
git clone --depth 1 https://github.com/amnezia-vpn/amneziawg-linux-kernel-module.git /tmp/amneziawg-module
cd /tmp/amneziawg-module/src
make dkms-install
dkms add -m amneziawg -v 1.0.0
dkms build -m amneziawg -v 1.0.0
dkms install -m amneziawg -v 1.0.0
modprobe amneziawg
cd ~ && rm -rf /tmp/amneziawg-module

# 2. Install AmneziaWG 2.0 tools (awg CLI)
git clone --depth 1 https://github.com/amnezia-vpn/amneziawg-tools.git /tmp/amneziawg-tools
make -C /tmp/amneziawg-tools/src && make -C /tmp/amneziawg-tools/src install
rm -rf /tmp/amneziawg-tools

# 3. Enable IP forwarding
sysctl -w net.ipv4.ip_forward=1
echo "net.ipv4.ip_forward=1" >> /etc/sysctl.conf

# 4. Download latest awg-server
curl -fsSL https://github.com/stealthsurf-vpn/awg-server/releases/latest/download/awg-server-linux-$(uname -m | sed 's/x86_64/amd64/' | sed 's/aarch64/arm64/') -o /usr/local/bin/awg-server
chmod +x /usr/local/bin/awg-server

# 5. Create data directory
mkdir -p /data

# 6. Create systemd service
cat > /etc/systemd/system/awg-server.service <<EOF
[Unit]
Description=AmneziaWG Server
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/awg-server
Restart=always
RestartSec=5

Environment=AWG_API_TOKEN=your-secret-token
Environment=AWG_ADDRESS=10.0.0.1/24
Environment=AWG_ENDPOINT=your.server.ip
Environment=AWG_JC=5
Environment=AWG_JMIN=50
Environment=AWG_JMAX=1000

[Install]
WantedBy=multi-user.target
EOF

# 7. Start and enable on boot
systemctl daemon-reload
systemctl enable --now awg-server
```

Check status:

```bash
systemctl status awg-server
journalctl -u awg-server -f
```

## Prerequisites

- [amneziawg-linux-kernel-module](https://github.com/amnezia-vpn/amneziawg-linux-kernel-module) installed on host
- [amneziawg-tools](https://github.com/amnezia-vpn/amneziawg-tools) (`awg` CLI) installed on host
- `iptables`, `iproute2` (usually already present)
- `net.ipv4.ip_forward=1` sysctl enabled

## CLI Commands

```bash
# Check current version
awg-server version

# Self-update to latest GitHub release
awg-server update

# Start the server (default, no arguments)
awg-server
```

After `awg-server update`, restart the service: `systemctl restart awg-server`.

## Build

```bash
# Build for current platform (with version)
make build VERSION=1.0.0

# Build for all platforms (linux, darwin, windows × amd64, arm64)
make build-all

# Static analysis
make vet

# Clean build artifacts
make clean
```

Requires Go 1.24+. Binaries are output to `dist/`.

## Deploy

Copy `awg-server` binary to the VPN server and run:

```bash
AWG_API_TOKEN=your-secret-token \
AWG_ADDRESS=10.0.0.1/24 \
AWG_ENDPOINT=your.server.ip \
./awg-server
```

## API

All endpoints require `Authorization: Bearer <AWG_API_TOKEN>`.

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

# Create client with custom obfuscation params and port
curl -X POST http://localhost:7777/api/clients \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"id":"my-client-uuid","awg_params":{"port":51825,"jc":5,"jmin":50,"jmax":1000,"s1":40,"s3":20,"h1":500000}}'

# Update client obfuscation params
curl -X PATCH http://localhost:7777/api/clients/my-client-uuid \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"awg_params":{"port":51825,"jc":10,"jmin":100,"jmax":2000}}'

# Get client config (.conf)
curl http://localhost:7777/api/clients/my-client-uuid/configuration \
  -H "Authorization: Bearer $TOKEN"

# Delete client
curl -X DELETE http://localhost:7777/api/clients/my-client-uuid \
  -H "Authorization: Bearer $TOKEN"
```

## Configuration

Environment variables:

| Variable | Required | Default | Description |
| -------- | -------- | ------- | ----------- |
| `AWG_API_TOKEN` | yes | — | Bearer token for API auth |
| `AWG_ADDRESS` | yes | — | Server VPN address (CIDR), e.g. `10.0.0.1/24` |
| `AWG_ENDPOINT` | yes | — | Public IP/hostname for client configs |
| `AWG_LISTEN_PORT` | no | `51820` | Base WireGuard UDP port (auto-assigned sequentially; can be overridden per-client via `port` in `awg_params`) |
| `AWG_HTTP_PORT` | no | `7777` | HTTP API port |
| `AWG_MTU` | no | `1420` | MTU value |
| `AWG_DNS` | no | `1.1.1.1` | DNS for client configs |
| `AWG_DATA_DIR` | no | `/data` | Persistence directory |
| `AWG_INTERFACE` | no | auto-detect | Override outbound network interface for NAT |
| `AWG_MAX_INTERFACES` | no | `0` | Max AWG interfaces (0 = unlimited) |

### Obfuscation Parameters (AmneziaWG 2.0)

These env vars set **default** CPS parameters for clients that don't specify custom `awg_params`:

| Variable | What it does | Impact |
| -------- | ------------ | ------ |
| `AWG_JC` | Junk packets sent during handshake (noise for DPI) | More = harder to detect, slightly slower connect. **0** = off, **3-8** = good. No effect after connect. |
| `AWG_JMIN` / `AWG_JMAX` | Size range for junk packets (bytes) | Wider range = harder to fingerprint. **50-100 / 500-1000** = good. |
| `AWG_S1` | Extra bytes added to init handshake packet | Standard WireGuard init = 148 bytes, DPI looks for this. **15-150** = good. Only at connect time. |
| `AWG_S2` | Extra bytes added to response handshake packet | Standard response = 92 bytes. **15-150** = good. Only at connect time. Note: `S1 + 56` must not equal `S2` (otherwise padded init and response end up the same size). |
| `AWG_S3` | Extra bytes added to cookie reply packets | Standard cookie = 64 bytes. **0-32** = good. Only under load. |
| `AWG_S4` | Extra bytes added to **every** data packet | Adds overhead to **every** packet — use only if DPI blocks by data packet size. **0** = recommended for most cases. |
| `AWG_H1`-`AWG_H4` | Replace WireGuard message type headers with random values from a **range** | **Best protection for free.** Format: `min-max` (e.g. `100000-800000`). Ranges **must not overlap**. Zero performance impact. |
| `AWG_I1`-`AWG_I5` | CPS signature packets sent before each handshake | Decoy UDP packets that mimic another protocol (QUIC, DNS, SIP, etc). Uses [CPS tag format](https://github.com/amnezia-vpn/amneziawg-go#i-parameters). If `I1` is not set, the entire chain is skipped. |

### Auto-Generated Parameters

On first start, the server generates and persists unique obfuscation values:

- **H1-H4** — random from non-overlapping uint32 ranges (header masking, zero overhead)
- **S1, S2** — random 15-150 (handshake padding, `S1 + 56 ≠ S2`)

These are saved in `/data/clients.json` and reused across restarts. No env vars needed.

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
AWG_I1='<b 0xc0><r 32><c><t>'
```

Ping: same after connect (~100ms extra at handshake). Protection: maximum without per-packet overhead.

## Multi-Interface Architecture

AmneziaWG sets CPS obfuscation parameters at the **interface level**, not per-peer. To support per-client custom parameters, the server manages a **pool of interfaces**:

- Each unique set of CPS parameters gets its own `awgN` interface (awg0, awg1, awg2, ...)
- Clients with identical CPS parameters share an interface
- Each interface listens on its own UDP port (explicit `port` from `awg_params`, or auto-assigned sequentially from base port)
- Interfaces are created on demand and destroyed when their last peer is removed
- All interfaces share the same server private key

```text
main.go → config → awg (pool, params, keygen) → clients (manager, storage) → api (server, handlers)
```

- **Kernel module** — `amneziawg-linux-kernel-module` on host, `awg` CLI for management
- **Static binary** — `CGO_ENABLED=0`, no external Go dependencies beyond `golang.org/x/crypto`
- **Persistence** via JSON file with atomic writes
- **IP allocation** sequential from .2, freed IPs reusable
- **Auth** Bearer token on all endpoints
