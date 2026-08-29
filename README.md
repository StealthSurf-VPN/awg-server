# awg-server

`awg-server` is an HTTP API for managing mixed AmneziaWG 2.0 and AmneziaWG 3.1
VPN clients on Linux hosts. It manages AmneziaWG kernel interfaces through the
`awg` CLI and keeps client, profile, and usage state in JSON files.

New clients use AmneziaWG 3.1 by default. Existing persisted clients without a
version remain AmneziaWG 2.0 until an authenticated caller explicitly migrates
them. A host can therefore serve 2.0 and 3.1 clients at the same time on
separate profile-matched interfaces.

## Installer release gate

A source checkout alone is not an installable AWG 3.1 release. Do not use the
one-line command until this AWG 3.1 installer is present on `main`. Before
running it on an existing 2.0 host, also verify that the
[latest stable GitHub release](https://github.com/StealthSurf-VPN/awg-server/releases/latest)
contains the complete signed AWG 3.1 bundle: all six
`awg-server-awg31-*` binaries, `SHA256SUMS`, and `SHA256SUMS.sig`. If the latest
release still exposes the legacy asset set, the AWG 3.1 installer fails staging
before it disables or stops `awg-server.service`. Package installation and
version gates run before staging, however, so the host's packages may already
have changed.

## Quick install

The supported host is a root-controlled Ubuntu 22.04 host or VM, not a
container. Run the installer from a root shell:

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/StealthSurf-VPN/awg-server/main/scripts/install.sh)
```

The installer is the supported bridge from a 2.0 host to this major release. It
first installs and gates the required package version, then stages and verifies
a signed 3.1 release. Before the controlled stop it disables and verifies
automatic startup, and it proceeds only after systemd reports the exact stopped
state. It preserves the data directory, performs a bounded qualification of the
reloaded runtime while the unit is stopped and disabled, and verifies that the
probe left no AmneziaWG interface behind. The new service must pass both
`/health` and an authenticated `/api/clients` JSON-array request before the
installer enables it. A failure after the stop boundary requires manual
recovery and remains stopped and disabled across reboot when both systemd
states can be confirmed. The installer does not configure a public firewall
policy.

Read [the installation guide](docs/installation.md) before using it on a
production host. The guide defines the intentional outage, backup, recovery,
and remaining physical-client qualification gates.

## Compatibility and migration

`protocol_version` has canonical values `"2.0"` and `"3.1"`.

- API input also accepts `"2"` and normalizes it to `"2.0"`.
- Existing disk records without the field are legacy 2.0 only; they do not
  inherit the new default on restart.
- Every newly saved client has an explicit canonical version. An omitted
  `protocol_version` in `POST /api/clients` uses
  `AWG_DEFAULT_PROTOCOL_VERSION`, which defaults to `3.1`.
- `PATCH /api/clients/{id}` changes versions only when the caller explicitly
  supplies `protocol_version`; download and re-import the configuration after
  any migration, reset, or regeneration.

The server never writes a synthetic `ProtocolVersion` line to a client
configuration. The active 3.1 fields identify the wire configuration.

Use a client build that imports the generated 3.1 configuration. The repository
does not prove import, handshake, or throughput on physical Windows, macOS,
iOS, or Android devices; qualify those paths before rollout. A rollback to
v1.0.5 after a 3.1 client has been issued is unsupported.

## CLI commands

```bash
awg-server version
awg-server check-runtime
awg-server update
awg-server
```

`check-runtime` is a Linux host diagnostic. It verifies installed Ubuntu
packages, strict `awg --version` output, and an isolated 3.1 interface probe
before any normal startup creates a client-owned interface. The probe requests
a kernel-assigned UDP port, brings its randomized temporary interface up to
exercise the actual socket bind, and requires the assigned port and complete
configuration to survive readback. External commands are bounded; deletion of
an interface created, or ambiguously created by a timed-out `ip` command, uses
a separate bounded cleanup attempt.
Normal startup runs the same qualifier after pure persisted-state validation
and before pool, firewall, or HTTP work.

`update` is available only to official signed Linux and macOS release binaries.
It is upgrade-only and fails closed for unexpected asset names, manifests,
signatures, URLs, checksums, or versions. Windows self-update fails closed
before network access. Older updaters expect legacy asset names and therefore
fail closed against this AWG 3.1 release rather than selecting an ambiguous
binary; use the installer or a separately verified asset for the bridge.

## Build and deterministic checks

```bash
make build VERSION=1.0.0
make build-all VERSION=1.0.0
go test ./...
go test -race -count=1 ./...
go vet ./...
go build -o awg-server .
bash scripts/install_test.sh
bash scripts/release-marker_test.sh
bash scripts/release-notes_test.sh
bash scripts/release-previous-tag_test.sh
git diff --check
make clean
```

`make build` writes `./awg-server`. `make build-all` creates exactly these
release binaries under `dist/`:

```text
awg-server-awg31-darwin-amd64
awg-server-awg31-darwin-arm64
awg-server-awg31-linux-amd64
awg-server-awg31-linux-arm64
awg-server-awg31-windows-amd64.exe
awg-server-awg31-windows-arm64.exe
```

`SHA256SUMS` and `SHA256SUMS.sig` accompany the six binaries in an official
release. The CI workflow runs formatting and module checks, race-enabled Go
package tests, installer and release shell harnesses, `go vet`, workflow and
shell linting, and a build. Those checks do not prove an Ubuntu kernel module,
real handshakes, or physical-client import/throughput.

## API overview

Every `/api` route requires `Authorization: Bearer <AWG_API_TOKEN>`. `GET
/health` is intentionally unauthenticated.

| Method | Path | Success |
| --- | --- | --- |
| `GET` | `/health` | `200` |
| `GET` | `/api/capabilities` | `200` |
| `POST` | `/api/awg-params/generate` | `200` |
| `GET` | `/api/clients` | `200` |
| `POST` | `/api/clients` | `201` |
| `PATCH` | `/api/clients/lan-group` | `200` |
| `PATCH` | `/api/clients/{id}` | `200` |
| `POST` | `/api/clients/{id}/regenerate-awg-params` | `200` |
| `GET` | `/api/clients/{id}/configuration` | `200` |
| `GET` | `/api/clients/{id}/stats` | `200` |
| `DELETE` | `/api/clients/{id}` | `204` |

The successful capabilities response is:

```json
{"awg_protocol_3_1":true,"lan_group_isolation":true}
```

Ordinary client list/create/update responses always include
`protocol_version` but never expose `header_key_id` or a header-protection key.
The key is included only in the authenticated client configuration that needs it.

Example: create a 3.1 client using server defaults.

```bash
printf 'Authorization: Bearer %s\n' "$AWG_API_TOKEN" | \
  curl -X POST http://127.0.0.1:7777/api/clients \
  --header @- \
  -H 'Content-Type: application/json' \
  -d '{"id":"client-example","protocol_version":"3.1"}'
```

Example: explicitly migrate a client and reset its stored overrides to the
target defaults in one request.

```bash
printf 'Authorization: Bearer %s\n' "$AWG_API_TOKEN" | \
  curl -X PATCH http://127.0.0.1:7777/api/clients/client-example \
  --header @- \
  -H 'Content-Type: application/json' \
  -d '{"protocol_version":"3.1","awg_params":null}'
```

Full input, response, status, parameter, DNS, routing, and redaction details
are in [the API reference](docs/api.md).

## Operational limits

3.1 defaults use fixed generated H values and require every effective S1-S4
value to be at least 12. Fixed H generation avoids the known ranged-header
throughput/MAC1 risk in the target runtime; manually supplied valid ranges
remain supported. `DisableCookies` defaults to `off`, retaining cookie replies
as a denial-of-service defense.

Different effective server-side profile settings create separate `awgN`
interfaces. Profile identity includes the protocol version, private 3.1 header
key, and server-applied parameters; it excludes client-only MTU, DNS,
persistent keepalive, client listen port, I1-I5, routing, and peer PSKs. See
[configuration](docs/configuration.md) for persistence and interface details.

The service requires a kernel module and a compatible client runtime. Local
tests cover selected parsing, API, persistence, runtime/pool transaction, and
installer/release contracts; they do not emulate a real kernel or client.
Before production use, qualify Ubuntu 22.04 amd64 and arm64 hosts, module
build/load, runtime probe, authenticated API gate, client import, handshake,
and throughput.
