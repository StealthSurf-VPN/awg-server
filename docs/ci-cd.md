# CI, Release, and Deployment

## Continuous Integration

`.github/workflows/ci.yml` runs for pull requests targeting `main`, pushes to `main`, merge queue checks, and manual dispatches. The required `Verify` job performs:

- repository-wide `gofmt` verification;
- `go mod verify`;
- pinned `actionlint` and `ShellCheck` containers for workflow and shell automation;
- release marker and exact changelog-section parser tests;
- deployment helper signature, architecture, version, downgrade, interruption, health-failure, restart-failure, and rollback tests in an isolated Docker container;
- `go test -race -count=1 ./...`, including signed self-update success, tampering, RSA-key, exact-asset, version, and downgrade cases;
- `go vet ./...`;
- a clean `go build` outside the checkout;
- `git diff --check` and tracked working-tree verification.

`internal/api/server_test.go` exercises the real `ServeMux`, handlers, client manager, temporary JSON storage, routing and DNS normalization, configuration rendering, key generation, and usage collector. Only the host device boundary is replaced. The test covers every registered API operation:

| Method | Path |
| --- | --- |
| `GET` | `/health` |
| `POST` | `/api/awg-params/generate` |
| `GET` | `/api/clients` |
| `POST` | `/api/clients` |
| `PATCH` | `/api/clients/{id}` |
| `POST` | `/api/clients/{id}/regenerate-awg-params` |
| `GET` | `/api/clients/{id}/configuration` |
| `GET` | `/api/clients/{id}/stats` |
| `DELETE` | `/api/clients/{id}` |

The API suite also checks authentication for every protected operation, validation failures, missing clients, wrong methods, duplicate clients, interface limits, migration conflicts, generic internal errors, snapshot-before-migration behavior, exact routing and DNS output, PSK isolation, usage cleanup, and device cleanup.

Run the same deterministic checks locally:

```bash
bash scripts/release-marker_test.sh
bash scripts/release-notes_test.sh
bash scripts/deploy-awg-server_test.sh
go test -race -count=1 ./...
go vet ./...
go build -trimpath -o /tmp/awg-server .
git diff --check
```

The Go API tests intentionally do not execute `awg`, `ip`, or `iptables`. A real VPN handshake requires a Linux host with the matching AmneziaWG kernel/userspace implementation, network capabilities, and `/dev/net/tun`; it is a separate integration gate and must not be represented as kernel coverage on a generic GitHub-hosted runner.

## Automated Releases

A release is requested by adding one strict marker to the immutable HEAD commit message that lands on `main`:

```text
release:v1.1.0
```

The marker has the following contract:

- only stable `vMAJOR.MINOR.PATCH` versions are accepted;
- numeric components cannot have leading zeroes, except the value `0` itself;
- prerelease and build suffixes are rejected;
- repeated occurrences of the same tag in the commit message are deduplicated;
- different tags in the same release context are an error;
- mutable pull request metadata is never used as release authorization;
- for a pull request release, put the marker in the title and confirm that the final merge/squash commit message preserves it before merging;
- a marker that exists only in an open or merged PR body never publishes anything;
- publication starts only after merge/push to `main` and a successful `Verify` job;
- the requested version must be newer than every existing stable GitHub Release;
- `CHANGELOG.md` must contain exactly one non-empty dated `## [MAJOR.MINOR.PATCH] - YYYY-MM-DD` section.

No marker means normal CI with no tag or release side effect. The change that requests `v1.1.0` must also add:

```markdown
## [1.1.0] - 2026-07-15

- Added per-client bypass routing, DNS modes, and AWG parameter regeneration.
```

The marker starts the automated release pipeline, but the signing job is protected by the `release-signing` GitHub Environment and waits for its required reviewer. Publication remains automatic after that approval. This gate prevents an unreviewed `main` change from immediately receiving the long-lived private key.

After validation, the workflow:

1. validates the configured Ed25519 public key, then builds Linux, macOS, and Windows binaries for `amd64` and `arm64` with both `VERSION` and that release trust key embedded;
2. verifies the exact six-asset set, the runnable Linux `amd64` version, an independently expected linker key and its presence in every real artifact, and `SHA256SUMS`;
3. uploads an unsigned build artifact from the source checkout job;
4. in a separate job with no source checkout, revalidates the exact artifact contents, checksums, and embedded public key, requires the private key and configured public key to be one matching Ed25519 pair, and signs only `SHA256SUMS`;
5. transfers the signed bundle through a second immutable Actions artifact to the least-privilege publish job;
6. creates or validates an annotated `vMAJOR.MINOR.PATCH` tag pointing to the exact `main` commit;
7. publishes a non-draft, non-prerelease GitHub Release using the matching changelog section;
8. verifies the published metadata and byte-for-byte equality of all eight release assets with the signed bundle.

Release assets are:

```text
awg-server-darwin-amd64
awg-server-darwin-arm64
awg-server-linux-amd64
awg-server-linux-arm64
awg-server-windows-amd64.exe
awg-server-windows-arm64.exe
SHA256SUMS
SHA256SUMS.sig
```

If tag creation or post-publication verification is interrupted, rerunning the same workflow is safe. The publish job accepts only the expected annotated tag and, when a stable release already exists, only the latest release whose complete asset set is byte-for-byte identical to the newly verified bundle. Any mismatched tag, older release, or changed asset is rejected.

### Signed self-update contract

Official Linux and macOS release binaries embed the configured Ed25519 public key. `awg-server update` accepts only a strictly newer stable `vMAJOR.MINOR.PATCH` GitHub Release and requires exactly one host binary, `SHA256SUMS`, and `SHA256SUMS.sig` at their canonical, case-sensitive, version-bound repository URLs. Before replacing anything, it:

1. rejects unsupported platforms or a missing trust key before network access;
2. limits the latest-release JSON response to 1 MiB, validates a stable version, then rejects an equal version or downgrade before downloading any release asset;
3. for a newer version, validates the exact asset URLs; `Apply` then validates the update result again and parses the embedded key as exactly one Ed25519 public PEM;
4. acquires the same non-blocking interprocess lock used by the deployment helper and checks the actual installed binary version before any release asset download;
5. limits `SHA256SUMS` to 64 KiB and `SHA256SUMS.sig` to exactly 64 bytes, then verifies the Ed25519 signature over the complete manifest;
6. requires the manifest to contain the exact six release binaries in canonical order and format;
7. limits the selected binary to 64 MiB and checks its checksum;
8. executes only that signed temporary file with `version` and requires the exact expected version output;
9. rechecks the on-disk version under the lock and replaces the current executable only after every check succeeds.

Ordinary source builds omit the release trust key, so their `update` command fails closed. Windows also fails closed before network access because an active `.exe` cannot be replaced atomically; use a separately verified signed asset there. A binary produced before this signed-updater contract cannot gain retroactive authenticity from the new code; bootstrap it with a release verified out of band or through the protected deployment helper, with no legacy updater process still running. After that bootstrap, later Linux/macOS self-updates use the embedded trust anchor.

## Protected Automatic Deployment

Automatic deployment is disabled by default. A successful automated release calls `.github/workflows/deploy.yml` directly only when the repository variable below is enabled:

```text
AWG_AUTO_DEPLOY_ENABLED=true
```

The direct reusable-workflow call is intentional. GitHub does not start most new workflow runs for events created with the repository `GITHUB_TOKEN`, so relying only on a separate `release: published` trigger could skip an automatically created release.

The deployment targets one existing Linux systemd installation. It requires an annotated release tag whose commit is reachable from `main`, downloads the exact architecture asset, and verifies it against `SHA256SUMS`. The workflow then connects with strict host-key checking and streams the version, asset name, signed manifest, signature, and binary to a forced SSH command.

The root-owned helper acquires `/usr/local/bin/awg-server.update.lock`, caps uploads at 64 MiB, refuses any trust key that is not explicitly Ed25519, and verifies the signature against that root-owned key before it executes or installs the upload. The CLI updater uses the same advisory lock, so concurrent cooperative updates fail without downloading release assets or mutating anything. The helper also verifies the exact six-binary manifest, checksum, host architecture, embedded version, and that the requested version is not older than the installed stable version. Only then does it atomically replace `/usr/local/bin/awg-server`, restart `awg-server.service`, and check `http://127.0.0.1:7777/health`. A failed restart, health check, `SIGHUP`, `SIGINT`, or `SIGTERM` triggers restoration of the previous binary. `SIGKILL` cannot be trapped, so the rollback copy is deliberately retained until commit; if restoration itself fails, the helper preserves that recovery binary and reports its exact path.

### 1. Create the release signing key

On a trusted administrator machine with OpenSSL 3.x:

```bash
umask 077
openssl genpkey -algorithm ED25519 -out awg-release-signing-private.pem
openssl pkey \
  -in awg-release-signing-private.pem \
  -pubout \
  -out awg-release-signing-public.pem
openssl pkey -pubin \
  -in awg-release-signing-public.pem \
  -text -noout
openssl base64 -A \
  -in awg-release-signing-public.pem \
  > awg-release-signing-public.pem.b64
```

The public-key inspection must start with `ED25519 Public-Key:`. Configure the protected signing Environment and repository public variable as follows.

Create a GitHub Environment named `release-signing`, restrict its deployment branch to `main`, and configure at least one required reviewer. Then configure:

| Scope and type | Name | Value |
| --- | --- | --- |
| `release-signing` Environment secret | `AWG_RELEASE_SIGNING_PRIVATE_KEY` | Complete private PEM |
| Repository variable | `AWG_RELEASE_SIGNING_PUBLIC_KEY` | Exact single-line contents of `awg-release-signing-public.pem.b64` |

Never commit the private key, upload it as an artifact or release asset, or copy it to the VPN server. The public value is deliberately not secret: it is embedded in every official release binary. The package job sees only this public value. After Environment approval, the private secret is exposed only to an inline signing step in a separate job that has no source checkout, no `GITHUB_TOKEN` permissions, and does not execute the built binaries or repository scripts. Signing stops unless both configured values parse as Ed25519 and form the same keypair.

The Environment gate does not make same-repository workflow code cryptographically independent: an approved malicious YAML change could still misuse the secret. The reviewer must inspect the exact release commit, especially `.github/workflows/ci.yml`, before approving. A fully unattended release with stronger isolation requires an externally administered signer or KMS whose OIDC/provenance policy is pinned to separately controlled workflow code; that external system is intentionally outside this repository.

Copy only the public key to the target through a trusted administrative channel, then install it as the helper's root-owned trust anchor:

```bash
install -d -o root -g root -m 0755 /etc/awg-server
install -o root -g root -m 0644 \
  awg-release-signing-public.pem \
  /etc/awg-server/release-signing-public.pem
```

The signing secret, repository public-key variable, embedded updater key, and target public key must normally describe the same keypair. Any mismatch safely stops publication or deployment before an uploaded binary runs.

Do not rotate these values independently. Existing release binaries trust the old embedded key. A safe rotation requires a reviewed bridge release that is signed with the old private key but built with the new public key in `RELEASE_PUBLIC_KEY`; deploy that bridge through the still-old target trust, confirm every installation has it, then replace the target public key and both GitHub values. Systems that miss the bridge must be bootstrapped manually with a separately verified binary. The standard automated workflow intentionally enforces one matching keypair and therefore cannot silently create a bridge release.

### 2. Install the root-owned helper

On the target server, from a trusted checkout of the same release branch:

```bash
install -o root -g root -m 0755 \
  scripts/deploy-awg-server \
  /usr/local/sbin/deploy-awg-server
```

The supplied helper intentionally uses fixed production paths and names:

```text
binary:  /usr/local/bin/awg-server
service: awg-server
health:  http://127.0.0.1:7777/health
trust:   /etc/awg-server/release-signing-public.pem
```

The target must provide Bash, OpenSSL 3.x, GNU coreutils (`base64`, `sha256sum`, and `stat`), util-linux `flock`, `curl`, and `systemctl`. If the installation uses a different path, unit name, or `AWG_HTTP_PORT`, install a reviewed root-owned copy with those constants adjusted before enabling deployment.

### 3. Create a restricted deployment account

```bash
useradd --system --create-home --shell /bin/bash awg-deploy
install -d -o awg-deploy -g awg-deploy -m 0700 /home/awg-deploy/.ssh
```

Add the dedicated public key to `/home/awg-deploy/.ssh/authorized_keys` as one line:

```text
restrict,command="sudo -n /usr/local/sbin/deploy-awg-server" ssh-ed25519 AAAA... awg-server-deploy
```

Then secure the file:

```bash
chown awg-deploy:awg-deploy /home/awg-deploy/.ssh/authorized_keys
chmod 0600 /home/awg-deploy/.ssh/authorized_keys
```

Allow only the root-owned helper through sudo:

```bash
printf '%s\n' \
  'awg-deploy ALL=(root) NOPASSWD: /usr/local/sbin/deploy-awg-server' \
  > /etc/sudoers.d/awg-server-deploy
chmod 0440 /etc/sudoers.d/awg-server-deploy
visudo -cf /etc/sudoers.d/awg-server-deploy
```

The SSH key cannot select another command, request a PTY, or enable forwarding. Do not remove the forced command or replace the dedicated key with a general administrator key.

### 4. Configure the `production` GitHub Environment

Create a GitHub Environment named `production`. Restrict deployment to `main` and configure at least one required reviewer. This keeps both automatic post-release deployment and manual dispatch behind an explicit production approval.

Environment secrets:

| Name | Value |
| --- | --- |
| `AWG_DEPLOY_HOST` | DNS host name of the target; IPv4 literals are also accepted |
| `AWG_DEPLOY_USER` | `awg-deploy` |
| `AWG_DEPLOY_SSH_PRIVATE_KEY` | Dedicated private key matching `authorized_keys` |
| `AWG_DEPLOY_KNOWN_HOSTS` | Pinned OpenSSH `known_hosts` line obtained through a trusted channel |

Environment variables:

| Name | Default | Allowed values |
| --- | --- | --- |
| `AWG_DEPLOY_ARCH` | `amd64` | `amd64`, `arm64` |
| `AWG_DEPLOY_PORT` | `22` | `1` through `65535` |

Do not generate `AWG_DEPLOY_KNOWN_HOSTS` with `ssh-keyscan` inside the workflow. Verify the host fingerprint independently, then store the complete pinned line as the Environment secret.

### 5. Enable or run deployment

After the Environment and target are ready, create the repository-level variable `AWG_AUTO_DEPLOY_ENABLED=true`. Leave it absent or set to `false` to publish releases without deploying them.

An operator can also select the `Deploy awg-server` workflow and manually dispatch a signed stable release produced by this automation. Legacy unsigned or lightweight-tag releases are intentionally ineligible. Manual deployment uses the same annotated-tag ancestry, signed-manifest, checksum, architecture, Environment approval, SSH, version, health, interruption, and rollback gates. The helper rejects a version lower than the currently installed stable version; an intentional emergency rollback remains a separate root-admin operation and cannot be smuggled through ordinary workflow dispatch.

## Recommended Repository Settings

- Protect `main` and require pull requests plus the `Verify` check.
- Deny force-push and branch deletion.
- Require CODEOWNERS review for `.github/workflows/**`, `Makefile`, `internal/update/**`, `scripts/deploy-awg-server`, and this deployment documentation; deny unreviewed direct pushes to `main` because workflow code controls the signing step.
- Keep the default `GITHUB_TOKEN` read-only; the publish job alone requests `contents: write`.
- Allow only reviewed actions. The workflow pins every GitHub-maintained action to a full immutable commit SHA.
- Store `AWG_RELEASE_SIGNING_PRIVATE_KEY` only as a protected `release-signing` Environment secret; store the canonical base64 public PEM as repository variable `AWG_RELEASE_SIGNING_PUBLIC_KEY`; keep the same public key root-owned on the target.
- Restrict `release-signing` to `main`, require a reviewer, and inspect the exact workflow diff before releasing the key.
- Configure the `production` Environment, branch restriction, and required reviewer before enabling `AWG_AUTO_DEPLOY_ENABLED`.
- Enable immutable releases when available for the repository.
