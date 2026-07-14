# Installation Guide

## Prerequisites

- Linux kernel 5.6+ (WireGuard support)
- Root access or `NET_ADMIN` capability
- `iptables`, `iproute2` (usually pre-installed)
- Go 1.24+ (for building from source)

## 1. Install AmneziaWG Kernel Module

### Ubuntu / Debian

```bash
apt install -y dkms linux-headers-$(uname -r) git make gcc

git clone https://github.com/amnezia-vpn/amneziawg-linux-kernel-module.git
cd amneziawg-linux-kernel-module/src
make
make install
modprobe amneziawg
```

Verify:

```bash
lsmod | grep amneziawg
```

### CentOS / RHEL / AlmaLinux

```bash
yum install -y dkms kernel-devel kernel-headers git make gcc

git clone https://github.com/amnezia-vpn/amneziawg-linux-kernel-module.git
cd amneziawg-linux-kernel-module/src
make
make install
modprobe amneziawg
```

### DKMS (auto-rebuild on kernel update)

```bash
cd amneziawg-linux-kernel-module/src
make dkms-install
```

This registers the module with DKMS so it rebuilds automatically after kernel updates.

## 2. Install awg CLI Tool

### Build from source

```bash
apt install -y git make gcc  # or yum install -y ...

git clone https://github.com/amnezia-vpn/amneziawg-tools.git
cd amneziawg-tools/src
make
make install
```

Verify:

```bash
awg --version
```

## 3. Build awg-server

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o awg-server .
```

For ARM servers (e.g. Oracle Cloud ARM):

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o awg-server .
```

Direct source builds intentionally do not contain the official release verification key, so `awg-server update` fails closed for them. Official Linux and macOS release binaries embed that Ed25519 key and verify the canonical version-bound asset URLs, signed six-asset checksum manifest, selected checksum, upgrade-only on-disk version, interprocess lock, size limit, and downloaded binary version before replacement. Windows self-update fails closed before network access because an active `.exe` cannot be replaced atomically. Bootstrap an existing legacy or source-built installation with a separately verified signed release before relying on Linux/macOS self-update.

## 4. Deploy

Copy binary to the server:

```bash
scp awg-server root@your-server:/usr/local/bin/
```

The repository also provides an opt-in GitHub Actions deployment that requires the trust key to be Ed25519, verifies the signed release manifest against that root-owned key, rejects downgrades, uses a restricted forced SSH command, performs an atomic replacement, checks the local health endpoint, and rolls back on restart, health, or handled-signal failure. Complete target prerequisites, signing-key setup, self-update trust, and secret requirements are documented in [CI, release, and deployment](ci-cd.md). Keep deployment disabled until the signing trust, `production` Environment, and restricted server account are configured.

## 5. Enable IP Forwarding

```bash
sysctl -w net.ipv4.ip_forward=1
echo "net.ipv4.ip_forward=1" >> /etc/sysctl.conf
```

## 6. Run

### Direct

```bash
AWG_API_TOKEN=your-secret-token \
AWG_ADDRESS=10.0.0.1/24 \
AWG_ENDPOINT=your.server.ip \
/usr/local/bin/awg-server
```

### systemd Service

Create `/etc/systemd/system/awg-server.service`:

```ini
[Unit]
Description=AmneziaWG Server
After=network.target

[Service]
Type=simple
ExecStartPre=/sbin/modprobe amneziawg
ExecStart=/usr/local/bin/awg-server
Restart=always
RestartSec=5
EnvironmentFile=/etc/awg-server.env

[Install]
WantedBy=multi-user.target
```

Create `/etc/awg-server.env`:

```bash
AWG_API_TOKEN=your-secret-token
AWG_ADDRESS=10.0.0.1/24
AWG_ENDPOINT=your.server.ip
AWG_LISTEN_PORT=51820
AWG_HTTP_PORT=7777
AWG_DNS=1.1.1.1
AWG_MTU=1420
AWG_DATA_DIR=/var/lib/awg-server
AWG_JC=5
AWG_JMIN=50
AWG_JMAX=1000
AWG_S3=0
AWG_S4=0
AWG_MAX_INTERFACES=0
```

H1-H4 and S1/S2 are generated on first start and persisted. `AWG_JC`, `AWG_JMIN`, `AWG_JMAX`, `AWG_S3`, and `AWG_S4` are server-wide defaults inherited by clients that do not override those fields. `AWG_DNS` and `AWG_MTU` are generated-client defaults; `AWG_LISTEN_PORT` is the base for automatic interface ports. `AWG_MAX_INTERFACES=0` means unlimited. The example overrides `AWG_DATA_DIR`; if omitted, it defaults to `/data`. `AWG_INTERFACE` defaults to automatic outbound-interface detection, and `AWG_I1` through `AWG_I5` default to empty.

Enable and start:

```bash
mkdir -p /var/lib/awg-server
systemctl daemon-reload
systemctl enable awg-server
systemctl start awg-server
systemctl status awg-server
```

Startup restores every persisted client before starting the HTTP listener. When persisted clients exist, missing top-level `server_private_key` or `generated_params`, an invalid or mismatched client keypair, invalid AWG/routing settings, or any interface/peer restoration failure is fatal instead of silently generating replacement state or skipping a client. Failure to bind the configured HTTP port also exits non-zero after cleanup, allowing the service manager to restart or report the unit as failed. Inspect `journalctl -u awg-server` if the service does not reach the listening log line; cleanup of interfaces created earlier in a failed restore is best-effort.

Client create/update/regeneration/delete operations return a generic HTTP `500` when device work or `clients.json` persistence fails and attempt to restore the previous device state. Rollback is not guaranteed if a second host command fails or the process crashes, so inspect `awg show`, `ip route`, and the service logs after such an error.

## 7. Firewall

Open the automatic WireGuard UDP range, open every explicit per-client `awg_params.port`, and restrict the HTTP API to the internal network. The example `51820:51840` only covers 21 automatic or explicitly selected ports; increase it or add individual rules to match the deployment.

### iptables

```bash
iptables -A INPUT -p udp --dport 51820:51840 -j ACCEPT  # range for multiple AWG interfaces
iptables -A INPUT -p tcp --dport 7777 -s 10.0.0.0/8 -j ACCEPT
iptables -A INPUT -p tcp --dport 7777 -j DROP
```

### ufw (Ubuntu)

```bash
ufw allow 51820:51840/udp  # range for multiple AWG interfaces
ufw allow from 10.0.0.0/8 to any port 7777
```

The unauthenticated `GET /health` endpoint is intended for monitoring. Every `/api` route requires the bearer token; keep TCP port 7777 off the public Internet even though application authentication is enabled.

## Troubleshooting

### Module not loading

```bash
dmesg | grep amnezia
modinfo amneziawg
```

If `modprobe amneziawg` fails, rebuild the module for your kernel version:

```bash
cd amneziawg-linux-kernel-module/src
make clean
make
make install
modprobe amneziawg
```

### awg command not found

Ensure `/usr/bin/awg` exists after `make install`. If installed to a different prefix:

```bash
which awg
ln -s /usr/local/bin/awg /usr/bin/awg
```

### Interface creation fails

```bash
ip link add awg0 type amneziawg
```

If this fails with "Operation not supported", the kernel module is not loaded. Run `modprobe amneziawg`.
