# awg-server

HTTP API server for managing **AmneziaWG 2.0** VPN clients. Uses the **AmneziaWG kernel module** on the host with the `awg` CLI tool — kernel-level performance with DPI obfuscation via CPS (Custom Protocol Signature).

Supports **per-client obfuscation profiles** — each unique set of CPS parameters gets its own AWG interface, created on demand.

## Quick Install (Linux)

One-liner that installs AmneziaWG, downloads the latest `awg-server` binary, and gets you ready to run:

```bash
# 1. Install AmneziaWG kernel module (DKMS)
apt update && apt install -y software-properties-common linux-headers-$(uname -r)
add-apt-repository -y ppa:amnezia/ppa
apt update && apt install -y amneziawg

# 2. Install AmneziaWG tools (awg CLI)
apt install -y build-essential git
git clone https://github.com/amnezia-vpn/amneziawg-tools.git /tmp/amneziawg-tools
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
Environment=AWG_S1=15
Environment=AWG_S2=15
Environment=AWG_H1=12345
Environment=AWG_H2=23456
Environment=AWG_H3=34567
Environment=AWG_H4=45678

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
AWG_JC=5 AWG_JMIN=50 AWG_JMAX=1000 \
AWG_S1=15 AWG_S2=15 \
AWG_H1=12345 AWG_H2=23456 AWG_H3=34567 AWG_H4=45678 \
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
  -d '{"id":"my-client-uuid","awg_params":{"port":51825,"jc":5,"jmin":50,"jmax":1000,"s1":15,"h1":12345}}'

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

### Obfuscation Parameters

These env vars set **default** CPS parameters for clients that don't specify custom `awg_params`:

| Variable | What it does | Impact |
| -------- | ------------ | ------ |
| `AWG_JC` | Junk packets sent during handshake (noise for DPI) | More = harder to detect, slightly slower connect. **0** = off, **3-8** = good. No effect after connect. |
| `AWG_JMIN` / `AWG_JMAX` | Size range for junk packets (bytes) | Wider range = harder to fingerprint. **50-100 / 500-1000** = good. |
| `AWG_S1` | Extra bytes added to init handshake packet | Standard WireGuard init = 148 bytes, DPI looks for this. **15-150** = good. Only at connect time. |
| `AWG_S2` | Extra bytes added to response handshake packet | Standard response = 92 bytes. **15-150** = good. Only at connect time. |
| `AWG_S3` / `AWG_S4` | Extra bytes for cookie / data packets | `S4` adds overhead to **every** packet — use only if DPI blocks by data packet size. **0** = recommended. |
| `AWG_H1`-`AWG_H4` | Replace WireGuard message type headers with random values | **Best protection for free.** Changes 4 bytes in headers, zero performance impact. Random uint32 values. |
| `AWG_I1`-`AWG_I5` | CPS signature packets (AmneziaWG 2.0) | Sent before handshake to mimic another protocol. Advanced feature for aggressive DPI. |

### Obfuscation Profiles

> **Rule of thumb:** `h1-h4` are free (zero overhead). `jc/s1/s2` only affect connection time. `s4` affects every packet — use with care.

**Minimum latency** — for gaming, VoIP, real-time apps. Only header masking, no extra packets:

```bash
AWG_H1=1504275961 AWG_H2=2038463950 AWG_H3=3719183628 AWG_H4=1404089105
```

Ping: same as plain WireGuard. Protection: blocks signature-based DPI (effective against most filters).

**Balanced** — for daily browsing. Headers + light junk at handshake:

```bash
AWG_JC=5 AWG_JMIN=50 AWG_JMAX=1000
AWG_S1=40 AWG_S2=40
AWG_H1=1504275961 AWG_H2=2038463950 AWG_H3=3719183628 AWG_H4=1404089105
```

Ping: same after connect (~50ms extra at handshake). Protection: blocks signature + size-based DPI.

**Maximum stealth** — for regions with aggressive DPI (China, Iran, Turkmenistan):

```bash
AWG_JC=8 AWG_JMIN=50 AWG_JMAX=1000
AWG_S1=80 AWG_S2=80
AWG_H1=1504275961 AWG_H2=2038463950 AWG_H3=3719183628 AWG_H4=1404089105
# Add CPS for AmneziaWG 2.0 if available:
# AWG_I1=... AWG_I2=... etc.
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
