# CI and Release Automation

## Continuous Integration

`.github/workflows/ci.yml` runs for pull requests targeting `main`, pushes to `main`, merge queue checks, and manual dispatches. The required `Verify` job performs:

- repository-wide `gofmt` verification;
- `go mod verify`;
- pinned `actionlint` and ShellCheck containers for workflow and shell automation;
- release marker and release-note parser tests;
- `bash scripts/install_test.sh`, the deterministic stubbed installer transaction
  harness;
- `go test -race -count=1 ./...`, including signed self-update success, tampering, RSA-key, exact-asset, version, and downgrade cases;
- `go vet ./...`;
- a clean `go build` outside the checkout;
- `git diff --check` and tracked working-tree verification.

Run the same deterministic checks locally:

```bash
bash scripts/release-marker_test.sh
bash scripts/release-notes_test.sh
bash scripts/release-previous-tag_test.sh
bash scripts/install_test.sh
go test -race -count=1 ./...
go vet ./...
go build -trimpath -o /tmp/awg-server .
git diff --check
```

These are deterministic source and stubbed-host checks. They do not qualify a
real Ubuntu 22.04 module/DKMS reload, `awg-server check-runtime`, systemd
transaction, live 2.0/3.1 handshakes, or client import/throughput. Before a
production release, qualify Ubuntu amd64 and arm64 hosts and physical Windows,
macOS, iOS, and Android client builds separately.

## Automated Releases

A release is requested by adding one strict marker to the immutable HEAD commit message that lands on `main`:

```text
release:v1.0.4
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

No marker means normal CI with no tag or release side effect. Preparing version `1.0.4` requires:

```markdown
## [1.0.4] - 2026-07-15

- Added per-client bypass routing, DNS modes, and AWG parameter regeneration.
```

The marker starts the automated release pipeline, but the signing job is protected by the `release-signing` GitHub Environment and waits for its required reviewer. Publication remains automatic after that approval. This gate prevents an unreviewed `main` change from immediately receiving the long-lived private key.

### Release description

The release description is generated from the exact dated changelog section. When a previous stable release exists, the workflow appends one clickable compare range:

```markdown
- Added per-client bypass routing, DNS modes, and AWG parameter regeneration.

**Full Changelog**: [v1.0.3...v1.0.4](https://github.com/StealthSurf-VPN/awg-server/compare/v1.0.3...v1.0.4)
```

The previous tag is resolved from published, non-draft, non-prerelease GitHub Releases before packaging. The publish job resolves it again under release concurrency and stops if the value changed, so an overlapping release cannot publish a stale compare range. Recovery of an existing release excludes the current tag when resolving its previous tag. A repository's first release has no `Full Changelog` line.

After validation, the workflow:

1. resolves and binds the previous stable release tag;
2. renders the exact changelog section and `Full Changelog` range;
3. validates the configured Ed25519 public key, requires the installer to embed that same key, then builds Linux, macOS, and Windows binaries for `amd64` and `arm64` with both `VERSION` and that release trust key embedded;
4. verifies the exact six-asset set, the runnable Linux `amd64` version, an independently expected linker key and its presence in every real artifact, and `SHA256SUMS`;
5. uploads an unsigned build artifact from the source checkout job;
6. in a separate job with no source checkout, revalidates the exact artifact contents, checksums, and embedded public key, requires the private key and configured public key to be one matching Ed25519 pair, and signs only `SHA256SUMS`;
7. creates or validates an annotated `vMAJOR.MINOR.PATCH` tag pointing to the exact `main` commit;
8. publishes a non-draft, non-prerelease GitHub Release and verifies its description and all eight release assets against the prepared bundle.

Release assets are:

```text
awg-server-awg31-darwin-amd64
awg-server-awg31-darwin-arm64
awg-server-awg31-linux-amd64
awg-server-awg31-linux-arm64
awg-server-awg31-windows-amd64.exe
awg-server-awg31-windows-arm64.exe
SHA256SUMS
SHA256SUMS.sig
```

If tag creation or post-publication verification is interrupted, rerunning the same workflow is safe. The publish job accepts only the expected annotated tag and, when a stable release already exists, only the latest release whose description and complete asset set match the newly verified bundle. Any mismatched tag, stale previous tag, changed description, older release, or changed asset is rejected.

### No automated deployment

The workflow ends after GitHub Release publication and verification. It does not connect to a server, hold production SSH credentials, restart a service, or install a binary. There is no `production` Environment or `AWG_AUTO_DEPLOY_ENABLED` switch. Operators install an exact verified release manually or use the signed `awg-server update` command on an already trusted installation.

## Signed Self-Update Contract

Official Linux and macOS release binaries embed the configured Ed25519 public
key. `awg-server update` accepts only a strictly newer stable
`vMAJOR.MINOR.PATCH` GitHub Release and requires exactly one AWG31-prefixed host
binary, `SHA256SUMS`, and `SHA256SUMS.sig` at their canonical, case-sensitive,
version-bound repository URLs. Before replacing anything, it:

1. rejects unsupported platforms or a missing trust key before network access;
2. limits the latest-release JSON response to 1 MiB, validates a stable version, then rejects an equal version or downgrade before downloading any release asset;
3. for a newer version, validates the exact asset URLs; `Apply` then validates the update result again and parses the embedded key as exactly one Ed25519 public PEM;
4. acquires a non-blocking interprocess update lock and checks the actual installed binary version before any release asset download;
5. limits `SHA256SUMS` to 64 KiB and `SHA256SUMS.sig` to exactly 64 bytes, then verifies the Ed25519 signature over the complete manifest;
6. requires the manifest to contain the exact six release binaries in canonical order and format;
7. limits the selected binary to 64 MiB and checks its checksum;
8. executes only that signed temporary file with `version` and requires the exact expected version output;
9. rechecks the on-disk version under the lock and replaces the current executable only after every check succeeds.

Ordinary source builds omit the release trust key, so their `update` command
fails closed. Windows also fails closed before network access because an active
`.exe` cannot be replaced atomically; use a separately verified signed asset
there. Older updaters select only the legacy non-AWG31 asset names and therefore
fail closed against an AWG 3.1 release. The installer is the supported bridge
for a 2.0 host because it qualifies the package and loaded runtime before
replacement. A v1.0.5 downgrade after issuing a 3.1 client is unsupported: it
does not preserve the 3.1 private profile state on a later save.

## Release Signing Setup

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

The public-key inspection must start with `ED25519 Public-Key:`. Create a GitHub Environment named `release-signing`, restrict it to `main`, and configure at least one required reviewer. Then configure:

| Scope and type | Name | Value |
| --- | --- | --- |
| `release-signing` Environment secret | `AWG_RELEASE_SIGNING_PRIVATE_KEY` | Complete private PEM |
| Repository variable | `AWG_RELEASE_SIGNING_PUBLIC_KEY` | Exact single-line contents of `awg-release-signing-public.pem.b64` |

Never commit the private key, upload it as an artifact or release asset, or copy it to a VPN server. The public value is deliberately not secret: it is embedded in every official release binary and may be distributed out of band for bootstrap verification. The package job sees only this public value. After Environment approval, the private secret is exposed only to an inline signing step in a separate job that has no source checkout, no `GITHUB_TOKEN` permissions, and does not execute the built binaries or repository scripts. Signing stops unless both configured values parse as Ed25519 and form the same keypair.

The Environment gate does not make same-repository workflow code cryptographically independent: an approved malicious YAML change could still misuse the secret. The reviewer must inspect the exact release commit, especially `.github/workflows/ci.yml`, before approving. A fully unattended release with stronger isolation requires an externally administered signer or KMS whose OIDC/provenance policy is pinned to separately controlled workflow code; that external system is intentionally outside this repository.

Do not rotate the secret and public variable independently. Existing release binaries trust the old embedded key. A safe rotation requires a reviewed bridge binary signed by the old private key but built with the new public key, manual verified installation of that bridge on every server, and only then replacement of both GitHub values. Systems that miss the bridge must be bootstrapped manually. The standard automated workflow intentionally enforces one matching keypair and therefore cannot silently produce a bridge release.

## Recommended Repository Settings

- Protect `main` and require pull requests plus the `Verify` check.
- Deny force-push and branch deletion.
- Require CODEOWNERS review for `.github/workflows/**`, `Makefile`, `internal/update/**`, and this release documentation; deny unreviewed direct pushes to `main` because workflow code controls the signing step.
- Keep the default `GITHUB_TOKEN` read-only; the publish job alone requests `contents: write`.
- Allow only reviewed actions. The workflow pins every GitHub-maintained action to a full immutable commit SHA.
- Store `AWG_RELEASE_SIGNING_PRIVATE_KEY` only as a protected `release-signing` Environment secret and the canonical base64 public PEM as repository variable `AWG_RELEASE_SIGNING_PUBLIC_KEY`.
- Restrict `release-signing` to `main`, require a reviewer, and inspect the exact workflow diff before releasing the key.
- Enable immutable releases when available for the repository.

The manual signed-release fallback and its verification commands are documented in `.ai/rules/release.md`. Use it only when GitHub Actions cannot be recovered and the user explicitly requests the external release side effects.
