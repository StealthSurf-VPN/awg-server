# Installation Guide

## Supported host and external gates

The installer supports a root-controlled Ubuntu 22.04 host or VM on `x86_64`
or `aarch64`/`arm64`. Containers are rejected because their kernel-module state
is not independently controlled. It is not a production proof for another OS,
kernel, or architecture.

Before a production rollout, independently qualify both Ubuntu amd64 and arm64
hosts: package installation, DKMS build/load, `awg-server check-runtime`, the
authenticated API gate, and a real VPN handshake. Also verify import,
handshake, and throughput with the actual supported client releases on physical
Windows, macOS, iOS, and Android. Local Go and shell-harness tests do not prove
those external gates.

## Recommended installer

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/StealthSurf-VPN/awg-server/main/scripts/install.sh)
```

The bootstrap script is fetched from `main`; its embedded Ed25519 public key
authenticates the downloaded release bundle. The script prompts for the API
token without echo, endpoint, and VPN CIDR. It supports environment variables
in the process, then a safely parsed existing `/etc/awg-server.env`, then
interactive values for missing required settings. The process environment wins
even when it is empty.

The installer adds these 3.1 defaults when they are not already set:

```text
AWG_DEFAULT_PROTOCOL_VERSION=3.1
AWG31_MTU=1280
AWG31_PERSISTENT_KEEPALIVE=25-35
AWG31_CONTENT_PADDING_ADDITION=10-100
AWG31_REKEY_AFTER_TIME=100-120
AWG31_REKEY_TIMEOUT=3-7
AWG31_REJECT_AFTER_TIME=150-180
AWG31_KEEPALIVE_TIMEOUT=5-15
AWG31_MAX_HANDSHAKE_ATTEMPTS=15-20
AWG31_RANDOM_TRAILERS=on
AWG31_DISABLE_COOKIES=off
```

See [configuration](configuration.md) for every accepted setting and strict
validation rule. The installer chooses `/data` only when `/data/clients.json`
already exists; otherwise it uses `/var/lib/awg-server`. It preserves the
selected data directory on rerun and does not relocate data automatically.

## Controlled upgrade transaction

The installer intentionally creates a controlled service outage. Its order is:

1. Require root, Ubuntu 22.04, a host/VM, and a supported architecture.
2. Install the current AmneziaWG PPA packages and enforce the minimum package
   versions before staging a server binary.
3. Stage the exact architecture-specific AWG 3.1 asset under a root-only
   directory. Verify the Ed25519 signature, a canonical ordered manifest with
   the exact six asset names, selected checksum, and embedded binary version
   before touching the installed binary.
4. Stop `awg-server.service`. If stop cannot be confirmed, abort without
   assuming the process is down.
5. Create one root-only (`0700`) timestamped backup directory and copy the
   existing environment, `clients.json`, and `usage.json` when present as
   root-only files (`0600`). The backup intentionally does not contain the
   binary, unit, or sysctl file. Do not reuse a partial backup as evidence of
   recovery.
6. Refuse to proceed if any AmneziaWG interface remains after the service stop.
   The installer never deletes an unknown interface.
7. Require at least
   `amneziawg-tools` `1.0.20210914-0~202608130145+ee0f0a9~ubuntu22.04.1` and
   `amneziawg-dkms` `1.0.0-0~202608271845+b72bb7a~ubuntu22.04.1` on Ubuntu
   22.04. Reload the module and run the staged binary's `check-runtime` probe
   before replacing `/usr/local/bin/awg-server`.
8. Replace the binary only after the preceding gates, then write root-owned
   environment/unit/sysctl files and enable and start the unit.
9. Require a new systemd `InvocationID`, active unit, exact local
   `{"status":"ok"}` health response, and an authenticated `/api/clients`
   response that parses as a JSON array within the service deadline.

The API token is placed in a root-only curl configuration file for that gate;
it is not put in a curl command-line argument. Values containing CR or LF are
rejected before the environment or auth configuration is written.

If a post-replacement gate fails, the installer attempts to stop the service,
retains the backup, and does not automatically restore or restart an
unqualified binary. If it cannot confirm the stop, it says so rather than
claiming the service is down. The temporary staging directory is cleaned on
exit and is not recovery evidence. Recover manually from the retained root-only
backup after investigating logs, module state, and interfaces. This boundary
avoids claiming that a mixed package/kernel/service failure can be rolled back
safely.

## Migrating a legacy host

Use the installer—not the old self-updater—to migrate an existing 2.0 host.
It preserves `clients.json`; the server restores records without a
`protocol_version` as 2.0 and saves canonical metadata only after the whole
restore succeeds. Existing 2.0 client configurations continue to work until an
authenticated API caller explicitly migrates each client.

This major release accepts only the signed AWG31 asset set:

```text
awg-server-awg31-darwin-amd64
awg-server-awg31-darwin-arm64
awg-server-awg31-linux-amd64
awg-server-awg31-linux-arm64
awg-server-awg31-windows-amd64.exe
awg-server-awg31-windows-arm64.exe
```

An older updater requests the disjoint legacy asset names and therefore fails
closed instead of selecting a 3.1 binary. Do not bypass that bridge with an
old self-update path.

Newly issued clients default to 3.1. After a 3.1 client is issued, downgrading
the host to v1.0.5 is unsupported: that version has no 3.1 profile/key
contract, and a legacy updater expects old asset names. Keep the verified backup
only as recovery material; do not use it as an automated protocol downgrade.

## Manual installation

Manual installation is appropriate only when the operator can independently
verify the signed release and all host prerequisites.

```bash
apt-get update
apt-get install -y \
  build-essential ca-certificates curl dkms gnupg2 iproute2 iptables \
  libelf-dev openssl python3-launchpadlib software-properties-common \
  "linux-headers-$(uname -r)"
add-apt-repository -y ppa:amnezia/ppa
apt-get update
apt-get install -y amneziawg amneziawg-tools amneziawg-dkms
modprobe amneziawg
install -o root -g root -m 0755 <verified-awg-server> /usr/local/bin/awg-server
/usr/local/bin/awg-server check-runtime
```

`check-runtime` requires installed/OK package status, the configured minimum
package versions, strict `awg --version` output of the form
`amneziawg-tools v3.1.YYYYMMDD - https://amnezia.org`, and an isolated 3.1
setconf/showconf readback. It creates and deletes only a randomized temporary
interface that it created. A diagnostic module version may be unavailable; the
functional probe is authoritative.

Create `/etc/awg-server.env` with root-only permissions and real values outside
source control:

```bash
install -o root -g root -m 0600 /dev/null /etc/awg-server.env
editor /etc/awg-server.env
```

Use a systemd unit whose `ExecStartPre` loads `amneziawg`, whose `EnvironmentFile`
is `/etc/awg-server.env`, and whose `ExecStart` is
`/usr/local/bin/awg-server`. Enable IPv4 forwarding:

```bash
printf '%s\n' 'net.ipv4.ip_forward=1' > /etc/sysctl.d/99-awg-server.conf
sysctl -w net.ipv4.ip_forward=1
```

After starting or changing the service, confirm both the unauthenticated health
endpoint and the authenticated clients endpoint. Health alone does not prove
token loading or manager/API readiness.

```bash
curl -fsS http://127.0.0.1:7777/health
curl -fsS http://127.0.0.1:7777/api/clients \
  -H "Authorization: Bearer $AWG_API_TOKEN"
```

## Firewall and client handling

Allow the auto-assigned UDP range and every explicit per-client interface port.
Restrict the HTTP API to the operator's internal network even though it uses
bearer authentication. The service maintains an `AWG-LAN` chain that allows
only same-`lan_group_id` VPN peers and drops other VPN-subnet traffic between
`awg+` interfaces.

After a client version migration, parameter reset, or 3.1 regeneration, fetch
`GET /api/clients/{id}/configuration` again and re-import it on the client.
A 3.1 configuration contains the private header-protection key only in that
authenticated configuration response; do not copy it into tickets, logs, or
documentation.

## Troubleshooting boundaries

For an installer failure, keep the service stopped if the script says it could
not qualify the new runtime. Inspect the retained backup, then check:

```bash
systemctl --no-pager --full status awg-server.service
journalctl --no-pager -u awg-server.service -n 50
awg-server check-runtime
dkms status
modinfo amneziawg
```

Do not delete unknown `awg*` interfaces to make an installer rerun pass. Do not
claim recovery from a local shell test alone; re-run the authenticated service
gate and real client qualification after fixing a host problem.
