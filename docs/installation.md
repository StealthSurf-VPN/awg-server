# Installation Guide

## Target Platform

This guide targets Ubuntu 22.04 LTS running directly on a host or VM. AmneziaWG 2.0 requires Linux 4.14 or newer; Ubuntu 22.04 exceeds that requirement. Docker and LXC containers share the host kernel, so they cannot provide an independent kernel-module installation and are not supported for production deployment.

AmneziaVPN clients must be version 4.8.12.9 or newer. AmneziaWG 1.0 configurations are not upgraded in place; generate new client configurations after deploying this server.

## Prerequisites

- Root access
- Ubuntu 22.04 LTS with headers for the running kernel
- A trusted copy of the project's Ed25519 release public key
- `curl` to download the installer from an exact release tag
- Go 1.24+ only when building `awg-server` from source

## Recommended: verified installer

The installer supports Ubuntu 22.04 on `x86_64` and `aarch64`/`arm64` hosts or VMs. It must run as root and rejects containers. It installs `iptables`, `iproute2`, the AmneziaWG packages, and other host dependencies itself.

Choose an exact stable tag, such as `v1.2.3`. Do not download the installer from `main`, `latest`, or another mutable URL. The trusted Ed25519 public key must already be present on the host; the installer deliberately does not download its own trust anchor.

### Interactive installation

Download the script from the exact tag and run it as root. This example supplies the version and trusted-key path, then lets the installer prompt for the missing API token, VPN address, and public endpoint. The token is read without echoing it:

```bash
TAG=v1.2.3
curl -fsSL \
  "https://raw.githubusercontent.com/StealthSurf-VPN/awg-server/${TAG}/scripts/install.sh" \
  -o /tmp/awg-server-install.sh
sudo env \
  AWG_SERVER_VERSION="${TAG#v}" \
  AWG_RELEASE_PUBLIC_KEY_FILE=/root/awg-server-release-signing-public.pem \
  bash /tmp/awg-server-install.sh
```

The key can instead be stored at `/etc/awg-server/release-signing-public.pem`. When no key path is supplied, an existing file at that canonical path is used before the installer prompts for a path.

### Non-interactive installation

The configuration file is sourced by Bash as root. Create it from a trusted source, keep it root-owned and mode `0600`, and edit the token in the file instead of placing it in command arguments or shell history:

```bash
sudo install -o root -g root -m 0600 /dev/null /root/awg-server-install.env
sudoedit /root/awg-server-install.env
```

Example file contents:

```bash
AWG_SERVER_VERSION=1.2.3
AWG_RELEASE_PUBLIC_KEY_FILE=/root/awg-server-release-signing-public.pem
AWG_API_TOKEN='replace-in-editor'
AWG_ADDRESS=10.0.0.1/24
AWG_ENDPOINT=vpn.example.com
```

Download the installer from the matching exact tag and pass the file explicitly:

```bash
TAG=v1.2.3
curl -fsSL \
  "https://raw.githubusercontent.com/StealthSurf-VPN/awg-server/${TAG}/scripts/install.sh" \
  -o /tmp/awg-server-install.sh
sudo bash /tmp/awg-server-install.sh --config /root/awg-server-install.env
```

The installer accepts only `--config FILE` (plus `-h`/`--help`) as command-line options. The file must be a regular file owned by the effective user and must not be group- or world-writable; mode `0600` is recommended because it contains the bearer token.

### Inputs and precedence

Supported settings use this exact precedence, from highest to lowest:

1. Variables present in the installer's process environment
2. The file supplied with `--config`
3. An existing `/etc/awg-server.env`
4. Interactive prompts for any still-missing required setting

Process-environment presence wins over the two config files even when its value is empty, so an empty required value remains missing after the precedence merge instead of revealing a lower-precedence value. For `AWG_RELEASE_PUBLIC_KEY_FILE` only, the installer then uses `/etc/awg-server/release-signing-public.pem` when that path is a regular file. If that canonical-key fallback does not apply, the empty value is prompted for in a TTY or fails in non-interactive mode, like every other missing required setting.

The two installer-only settings are not written to the systemd environment file:

| Variable | Required | Description |
| -------- | -------- | ----------- |
| `AWG_SERVER_VERSION` | Yes | Exact stable `MAJOR.MINOR.PATCH` version without a leading `v`. Prereleases, `latest`, and malformed versions are rejected. |
| `AWG_RELEASE_PUBLIC_KEY_FILE` | Yes, unless the canonical key exists | Path to a trusted Ed25519 public PEM used to verify the signed release manifest. |

The following 20 settings are the complete service environment accepted by the installer. Required values have no default; omitted optional values use the shown server defaults.

| Variable | Required/default | Description |
| -------- | ---------------- | ----------- |
| `AWG_API_TOKEN` | Required | Bearer token for every `/api` route. |
| `AWG_ADDRESS` | Required | Server VPN IPv4 address and network in CIDR form, for example `10.0.0.1/24`. |
| `AWG_ENDPOINT` | Required | Public IP address or hostname written to client configurations. |
| `AWG_LISTEN_PORT` | `51820` | Base UDP port for automatically assigned AWG interfaces. |
| `AWG_HTTP_PORT` | `7777` | HTTP API listen port. |
| `AWG_MTU` | `1420` | Default MTU written to client configurations. |
| `AWG_DNS` | `1.1.1.1` | Default DNS server written to client configurations. |
| `AWG_DATA_DIR` | See below | Persistence directory for `clients.json` and `usage.json`. |
| `AWG_INTERFACE` | Auto-detect | Outbound interface used for MASQUERADE. |
| `AWG_JC` | `5` | Default junk packet count. |
| `AWG_JMIN` | `50` | Default junk packet minimum size. |
| `AWG_JMAX` | `1000` | Default junk packet maximum size. |
| `AWG_S3` | `0` | Default underload packet padding. |
| `AWG_S4` | `0` | Default transport packet padding. |
| `AWG_I1` | Empty | Default first CPS signature packet. |
| `AWG_I2` | Empty | Default second CPS signature packet. |
| `AWG_I3` | Empty | Default third CPS signature packet. |
| `AWG_I4` | Empty | Default fourth CPS signature packet. |
| `AWG_I5` | Empty | Default fifth CPS signature packet. |
| `AWG_MAX_INTERFACES` | `0` | Maximum AWG interface count; `0` means unlimited. |

See the [configuration reference](configuration.md) for validation and per-client override semantics. H1-H4, S1, S2, the server private key, and client PSKs are generated by the server and are not installer inputs.

When `AWG_DATA_DIR` is not set, the installer chooses an existing `/data` directory to preserve legacy state; otherwise it chooses `/var/lib/awg-server`. It writes the selected path to `/etc/awg-server.env`. A rerun first loads that file, keeps the selected data directory and its JSON files, and reuses the canonical release key when no higher-precedence value replaces either setting. Selecting a different data directory does not migrate old data.

### Installation and health gates

The installer:

1. Installs the official AmneziaWG package from the Amnezia PPA, including the XanMod LLVM 19 prerequisites when needed.
2. Downloads the architecture-specific binary, `SHA256SUMS`, and `SHA256SUMS.sig` only from `releases/download/v$AWG_SERVER_VERSION`.
3. Requires an Ed25519 public key, verifies the manifest signature, requires exactly one checksum for the selected asset, verifies that checksum, and checks the binary's reported version before installation.
4. Installs the binary at `/usr/local/bin/awg-server`, the key at `/etc/awg-server/release-signing-public.pem`, the root-only environment at `/etc/awg-server.env`, the forwarding sysctl at `/etc/sysctl.d/99-awg-server.conf`, and the unit at `/etc/systemd/system/awg-server.service`.
5. Enables and restarts `awg-server.service`, waits for a new active systemd invocation, and requires the exact `{"status":"ok"}` response from `http://127.0.0.1:$AWG_HTTP_PORT/health` within 30 seconds.

The command exits non-zero if any gate fails. A service-gate failure prints the full unit status and the last 50 journal lines. After a successful run, these checks should also pass:

```bash
/usr/local/bin/awg-server version
systemctl is-enabled --quiet awg-server.service
systemctl is-active --quiet awg-server.service
curl -fsS http://127.0.0.1:7777/health
```

Use the configured `AWG_HTTP_PORT` instead of `7777` when overridden. Rerunning the installer reinstalls the requested exact version and restarts the unit without deleting the selected data directory.

The installer does not configure inbound firewall policy or open or close ports. After the service starts, `awg-server` manages its own NAT/MASQUERADE rule as AWG interfaces are restored or created. The operator must expose every required AWG UDP port and restrict the HTTP API port to the internal network as described in [Firewall](#firewall).

## Manual alternative

Use the following steps only when the verified installer cannot be used or when building from source is intentional. They keep the same binary, environment, sysctl, data, and systemd paths as the automated installation.

### 1. Install AmneziaWG 2.0

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

### 2. Build awg-server

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o awg-server .
```

For ARM servers (e.g. Oracle Cloud ARM):

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o awg-server .
```

Direct source builds intentionally do not contain the official release verification key, so `awg-server update` fails closed for them. Official Linux and macOS release binaries embed that Ed25519 key and verify the canonical version-bound asset URLs, signed six-asset checksum manifest, selected checksum, upgrade-only on-disk version, interprocess lock, size limit, and downloaded binary version before replacement. Windows self-update fails closed before network access because an active `.exe` cannot be replaced atomically. Bootstrap an existing legacy or source-built installation with a separately verified signed release before relying on Linux/macOS self-update.

### 3. Deploy

Copy binary to the server:

```bash
scp awg-server root@your-server:/usr/local/bin/
```

### 4. Enable IP forwarding

```bash
printf '%s\n' 'net.ipv4.ip_forward=1' > /etc/sysctl.d/99-awg-server.conf
sysctl -w net.ipv4.ip_forward=1
```

### 5. Run

#### Direct

```bash
mkdir -p /var/lib/awg-server
read -r -s -p 'AWG_API_TOKEN: ' AWG_API_TOKEN
export AWG_API_TOKEN
export AWG_ADDRESS=10.0.0.1/24
export AWG_ENDPOINT=vpn.example.com
export AWG_DATA_DIR=/var/lib/awg-server
/usr/local/bin/awg-server
```

#### systemd service

Create `/etc/systemd/system/awg-server.service`:

```ini
[Unit]
Description=AmneziaWG Server
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
EnvironmentFile=/etc/awg-server.env
ExecStartPre=/sbin/modprobe amneziawg
ExecStart=/usr/local/bin/awg-server
Restart=on-failure
RestartSec=5

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

Set ownership and permissions before starting the unit:

```bash
chown root:root /etc/awg-server.env
chmod 0600 /etc/awg-server.env
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

## Firewall

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

### Installer or health gate fails

Service-gate failures already print `systemctl status` and the last 50 unit log lines. Recheck the same evidence after correcting the configuration:

```bash
systemctl --no-pager --full status awg-server.service
journalctl --no-pager -u awg-server.service -n 50
curl -fsS http://127.0.0.1:7777/health
```

Use the configured `AWG_HTTP_PORT` for the health request. Rerun the same exact-tag installer after fixing the cause; existing client and usage JSON in the selected data directory is preserved.

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
