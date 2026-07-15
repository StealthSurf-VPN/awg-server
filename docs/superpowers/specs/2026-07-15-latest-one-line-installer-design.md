# Latest One-Line Installer Design

## Goal

Install the latest stable `awg-server` release on Ubuntu 22.04 with one copy-paste command. The operator must not provide a release version or a public-key path.

## User flow

The documented command downloads the installer to a root-owned `mktemp` file and executes it only after `curl` succeeds:

```bash
sudo bash -c 'f=$(mktemp) || exit; trap "rm -f -- \"$f\"" EXIT; curl -fsSL https://raw.githubusercontent.com/StealthSurf-VPN/awg-server/main/scripts/install.sh -o "$f" && bash "$f"'
```

The installer asks only for:

- `AWG_API_TOKEN`, without terminal echo;
- `AWG_ENDPOINT`;
- `AWG_ADDRESS`, with `10.0.0.1/24` as the Enter default.

Those three values may still be supplied through the process environment for automation. Existing service settings in `/etc/awg-server.env` remain lower-precedence rerun defaults.

## Release selection and verification

`scripts/install.sh` contains the existing Ed25519 release public key as a public base64 constant. It downloads `SHA256SUMS` through the GitHub `releases/latest/download` redirect, accepts only the canonical final URL for a stable `vMAJOR.MINOR.PATCH` release, and uses that exact version-bound release URL for all remaining downloads.

The installer verifies the manifest signature, requires exactly one checksum for the selected architecture, verifies the binary checksum, and verifies the binary-reported version before installation. `AWG_SERVER_VERSION`, `AWG_RELEASE_PUBLIC_KEY_FILE`, `--config`, and the persisted standalone public-key file are removed.

Key rotation requires updating the constant in `scripts/install.sh`. The one-line bootstrap trusts delivery of `main/scripts/install.sh` over GitHub HTTPS; the embedded key protects the binary release path, not a compromised bootstrap source.

## Host behavior

AmneziaWG package installation, XanMod handling, forwarding, atomic environment/unit writes, legacy data preservation, systemd enable/restart, and the strict 30-second health gate remain unchanged.

## Verification

There is no dedicated installer test script. The change is checked with Bash syntax validation, pinned ShellCheck, pinned Actionlint, release automation checks, `go vet ./...`, the required Go build, and diff review.

