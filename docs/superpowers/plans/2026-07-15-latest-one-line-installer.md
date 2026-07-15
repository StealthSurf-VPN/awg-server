# Latest One-Line Installer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace version/key-file installer inputs with a single repository command that installs the latest signed stable release.

**Architecture:** Keep the existing host mutation and systemd flow. Resolve the latest stable version from GitHub's canonical redirect, decode the repository's existing public release key from a constant, and reuse the current signature/checksum/version gates.

**Tech Stack:** Bash 3.2-compatible syntax, Ubuntu 22.04 systemd/apt tooling, curl, OpenSSL, GitHub Releases.

## Global Constraints

- The public command is exactly `bash <(curl -fsSL https://raw.githubusercontent.com/StealthSurf-VPN/awg-server/main/scripts/install.sh)` and runs from a root shell.
- The operator supplies neither a release version nor a public-key path.
- Preserve signed-manifest verification, systemd setup, data persistence, and the 30-second health gate.
- Do not add a dedicated installer test file or a new dependency.

---

### Task 1: Collapse installer inputs and resolve the latest signed release

**Files:**
- Modify: `scripts/install.sh`

**Interfaces:**
- Consumes: GitHub's `releases/latest/download/SHA256SUMS` redirect and the existing Ed25519 release public key.
- Produces: `resolve_latest_version()` printing a stable `MAJOR.MINOR.PATCH`; `install_verified_release(asset)` installing the matching verified binary.

- [ ] **Step 1: Reduce installer configuration to service values**

Set the required prompt order and embed the verified public key:

```bash
readonly RELEASE_PUBLIC_KEY_BASE64='LS0tLS1CRUdJTiBQVUJMSUMgS0VZLS0tLS0KTUNvd0JRWURLMlZ3QXlFQW1PRDdZY01LZWE3Wm9XK2ZFSmZ0cEZ3NGNYcTVzdnp6QVBUb2tiY3dTNkE9Ci0tLS0tRU5EIFBVQkxJQyBLRVktLS0tLQo='
readonly LATEST_MANIFEST_URL='https://github.com/StealthSurf-VPN/awg-server/releases/latest/download/SHA256SUMS'
readonly -a REQUIRED_KEYS=(AWG_API_TOKEN AWG_ENDPOINT AWG_ADDRESS)
```

Delete `AWG_SERVER_VERSION` and `AWG_RELEASE_PUBLIC_KEY_FILE` from config arrays. Use `ENVIRONMENT_KEYS` directly when capturing/restoring process values, and delete `usage()` plus explicit config handling.

- [ ] **Step 2: Add canonical latest-version resolution**

Implement a HEAD request and accept only the repository's exact stable redirect:

```bash
resolve_latest_version() {
    local headers location
    local pattern='^https://github\.com/StealthSurf-VPN/awg-server/releases/download/v((0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*))/SHA256SUMS$'

    headers=$(curl -fsSI --proto '=https' --tlsv1.2 "$LATEST_MANIFEST_URL") \
        || die 'could not resolve the latest stable release'
    location=$(printf '%s\n' "$headers" | awk \
        'tolower($1) == "location:" { sub(/\r$/, "", $2); print $2; exit }')
    [[ $location =~ $pattern ]] \
        || die 'latest release redirect is not canonical'
    printf '%s\n' "${BASH_REMATCH[1]}"
}
```

In `main`, reject arguments, load only `/etc/awg-server.env`, restore process environment precedence, prompt for the three required service values, select the data directory, and assign:

```bash
AWG_SERVER_VERSION=$(resolve_latest_version)
```

- [ ] **Step 3: Replace the external key file with the embedded key**

In `install_verified_release`, decode the constant into the existing private temporary directory before the current Ed25519 type check:

```bash
if ! printf '%s' "$RELEASE_PUBLIC_KEY_BASE64" \
    | openssl base64 -d -A > "$verified_key"; then
    die 'could not decode the release public key'
fi
chmod 0600 "$verified_key"
```

Download `SHA256SUMS`, `SHA256SUMS.sig`, and then the selected binary from the exact `v$AWG_SERVER_VERSION` URL. Keep manifest signature, unique checksum, SHA-256, and reported-version checks. Stop installing `/etc/awg-server/release-signing-public.pem`.

- [ ] **Step 4: Run focused shell verification**

Run:

```bash
bash -n scripts/install.sh
docker run --rm -v "$PWD:/repo:ro" -w /repo \
  koalaman/shellcheck:v0.10.0@sha256:2097951f02e735b613f4a34de20c40f937a6c8f18ecb170612c88c34517221fb \
  scripts/install.sh
```

Expected: both commands exit `0` without diagnostics.

- [ ] **Step 5: Commit installer simplification**

```bash
git add scripts/install.sh
git commit -m "feat: install latest signed release automatically"
```

---

### Task 2: Replace installation documentation with the one-line flow

**Files:**
- Modify: `README.md`
- Modify: `docs/installation.md`

**Interfaces:**
- Consumes: the argument-free installer behavior from Task 1.
- Produces: one canonical root-shell command and accurate rerun/configuration documentation.

- [ ] **Step 1: Collapse README Quick Install**

Replace version, key-file, temporary-file, and `--config` examples with:

```bash
bash <(curl -fsSL https://raw.githubusercontent.com/StealthSurf-VPN/awg-server/main/scripts/install.sh)
```

State that the installer selects the latest stable signed release and prompts only for token, endpoint, and optional-default address. Remove the version/key requirement bullets.

- [ ] **Step 2: Align the installation guide**

Remove exact-tag, external-key, and explicit config-file sections. Document precedence as process environment, existing `/etc/awg-server.env`, then prompts. Update installation gates and rerun wording to say latest stable release, and remove the canonical installer-key path from installed outputs.

- [ ] **Step 3: Check stale contracts and formatting**

Run:

```bash
rg -n 'AWG_SERVER_VERSION|AWG_RELEASE_PUBLIC_KEY_FILE|--config|release-signing-public\.pem' \
  scripts/install.sh README.md docs/installation.md
git diff --check
```

Expected: no installer-input references; manual source-build release-key explanations may remain only when they describe `awg-server update`. `git diff --check` exits `0`.

- [ ] **Step 4: Run repository-required verification**

Run pinned Actionlint and ShellCheck from `.github/workflows/ci.yml`, the three existing release automation tests, `go vet ./...`, and `go build -o awg-server .`; remove the generated binary afterwards.

Expected: every command exits `0` and `git status --short` contains only the intended committed changes.

- [ ] **Step 5: Commit documentation**

```bash
git add README.md docs/installation.md
git commit -m "docs: make installer a one-line command"
```

