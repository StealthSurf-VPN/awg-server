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

## Build

```bash
# Build for current platform
make build

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
# List clients
curl http://localhost:7777/api/clients -H "Authorization: Bearer $TOKEN"

# Create client (default obfuscation params from env)
curl -X POST http://localhost:7777/api/clients \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"my-client-uuid"}'

# Create client with custom obfuscation params and port
curl -X POST http://localhost:7777/api/clients \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"my-client-uuid","awg_params":{"port":51825,"jc":5,"jmin":50,"jmax":1000,"s1":15,"h1":12345}}'

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
| `AWG_LISTEN_PORT` | no | `51820` | Base WireGuard UDP port (auto-assigned interfaces use +1, +2, ...; can be overridden per-client via `port` in `awg_params`) |
| `AWG_HTTP_PORT` | no | `7777` | HTTP API port |
| `AWG_MTU` | no | `1420` | MTU value |
| `AWG_DNS` | no | `1.1.1.1` | DNS for client configs |
| `AWG_DATA_DIR` | no | `/data` | Persistence directory |
| `AWG_MAX_INTERFACES` | no | `0` | Max AWG interfaces (0 = unlimited) |

### Default AmneziaWG Obfuscation

These env vars set **default** CPS parameters for clients that don't specify custom ones:

| Variable | Description |
| -------- | ----------- |
| `AWG_JC` | Junk packet count |
| `AWG_JMIN` / `AWG_JMAX` | Junk packet size range |
| `AWG_S1` - `AWG_S4` | Packet padding (init, response, underload, transport) |
| `AWG_H1` - `AWG_H4` | Packet headers (init, response, underload, transport) |
| `AWG_I1` - `AWG_I5` | CPS signature packets (AmneziaWG 2.0), e.g. `<b 0xc000000001><r 1200>` |

## Multi-Interface Architecture

AmneziaWG sets CPS obfuscation parameters at the **interface level**, not per-peer. To support per-client custom parameters, the server manages a **pool of interfaces**:

- Each unique set of CPS parameters gets its own `awgN` interface (awg0, awg1, awg2, ...)
- Clients with identical CPS parameters share an interface
- Each interface listens on its own UDP port (explicit `port` from `awg_params`, or auto-assigned as base port + index)
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
