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

## 4. Deploy

Copy binary to the server:

```bash
scp awg-server root@your-server:/usr/local/bin/
```

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
AWG_MAX_INTERFACES=0
```

H1-H4 and S1/S2 are auto-generated on first start and persisted. The AWG_* vars above are **defaults** for Jc/Jmin/Jmax only.

Enable and start:

```bash
mkdir -p /var/lib/awg-server
systemctl daemon-reload
systemctl enable awg-server
systemctl start awg-server
systemctl status awg-server
```

## 7. Firewall

Open WireGuard UDP port, restrict HTTP API to internal network:

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
