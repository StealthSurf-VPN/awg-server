# Installation Guide

## Target Platform

This guide targets Ubuntu 22.04 LTS running directly on a host or VM. AmneziaWG 2.0 requires Linux 4.14 or newer; Ubuntu 22.04 exceeds that requirement. Docker and LXC containers share the host kernel, so they cannot provide an independent kernel-module installation and are not supported for production deployment.

AmneziaVPN clients must be version 4.8.12.9 or newer. AmneziaWG 1.0 configurations are not upgraded in place; generate new client configurations after deploying this server.

## Prerequisites

- Root access
- Ubuntu 22.04 LTS with headers for the running kernel
- `iptables` and `iproute2`
- Go 1.24+ only when building `awg-server` from source

## 1. Install AmneziaWG 2.0

Install the official `amneziawg` metapackage. It installs both the DKMS kernel module and the `awg` CLI from the Amnezia PPA:

```bash
apt-get update
apt-get install -y \
  build-essential ca-certificates curl dkms gnupg2 iproute2 iptables \
  libelf-dev \
  python3-launchpadlib software-properties-common \
  "linux-headers-$(uname -r)"
```

If the running kernel is XanMod, install the matching LLVM 19 toolchain before installing AmneziaWG. Current XanMod kernels use LLVM-only build flags that the Ubuntu 22.04 default Clang 14 does not support:

```bash
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
```

Then enable the official Amnezia PPA and install the metapackage:

```bash
add-apt-repository -y ppa:amnezia/ppa
apt-get update
apt-get install -y amneziawg
```

Load and verify the installation:

```bash
modprobe amneziawg
modinfo amneziawg
awg --version
dkms status
```

The upstream DKMS and tools package versions use their own `1.0.*` numbering. That number is not the protocol version; the current official PPA packages support the AmneziaWG 2.0 fields used by `awg-server`, including ranged H1-H4 values, S3/S4, and I1-I5.

## 2. Build awg-server

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o awg-server .
```

For ARM servers (e.g. Oracle Cloud ARM):

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o awg-server .
```

Direct source builds intentionally do not contain the official release verification key, so `awg-server update` fails closed for them. Official Linux and macOS release binaries embed that Ed25519 key and verify the canonical version-bound asset URLs, signed six-asset checksum manifest, selected checksum, upgrade-only on-disk version, interprocess lock, size limit, and downloaded binary version before replacement. Windows self-update fails closed before network access because an active `.exe` cannot be replaced atomically. Bootstrap an existing legacy or source-built installation with a separately verified signed release before relying on Linux/macOS self-update.

## 3. Deploy

Copy binary to the server:

```bash
scp awg-server root@your-server:/usr/local/bin/
```

## 4. Enable IP Forwarding

```bash
sysctl -w net.ipv4.ip_forward=1
echo "net.ipv4.ip_forward=1" >> /etc/sysctl.conf
```

## 5. Run

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
Wants=network-online.target
After=network-online.target

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

## 6. Firewall

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
apt-get install --reinstall "linux-headers-$(uname -r)" amneziawg-dkms
dkms status
modprobe amneziawg
```

### awg command not found

Reinstall the official tools package and verify the binary:

```bash
apt-get install --reinstall amneziawg-tools
command -v awg
awg --version
```

### Interface creation fails

```bash
ip link add awg0 type amneziawg
```

If this fails with "Operation not supported", the kernel module is not loaded. Run `modprobe amneziawg`.
