# Release Workflow

GitHub Actions is the canonical release path. A successful push to `main` can publish an updater-compatible GitHub Release when the immutable HEAD commit message contains a strict release marker.

Release, tag, and push operations have external side effects. Do not perform them from a local agent session unless the user explicitly requests those side effects.

## Automated Release Request

Use exactly this stable marker shape:

```text
release:vX.Y.Z
```

Rules:

- `X`, `Y`, and `Z` are unsigned decimal integers without leading zeroes, except `0`.
- Prerelease and build metadata are not supported.
- The same tag can appear more than once in the HEAD commit message; identical occurrences are deduplicated.
- Different tags in the same release context fail the workflow.
- Mutable pull request metadata is not release authorization. A PR title marker works only when the final merge/squash commit message preserves it.
- Release detection runs only after push/merge to `main` and after `Verify` passes.
- The requested tag must be newer than every published stable release.
- `CHANGELOG.md` must contain exactly one non-empty `## [X.Y.Z] - YYYY-MM-DD` section.
- Release notes must contain that exact section followed by a clickable `Full Changelog` range from the previous stable release when one exists.

Test marker behavior locally:

```bash
bash scripts/release-marker_test.sh
bash scripts/release-notes_test.sh
bash scripts/release-previous-tag_test.sh
```

## Automated Gates

`.github/workflows/ci.yml` performs the following sequence:

1. Run formatting, module, shell automation, API/package race tests, vet, build, and diff checks.
2. Read only the immutable HEAD commit message from the exact `main` commit.
3. Parse the strict marker with `scripts/release-marker.sh`.
4. Resolve the previous published stable release and extract the exact dated changelog section plus its `Full Changelog` compare range with `scripts/release-notes.sh`.
5. Validate `AWG_RELEASE_SIGNING_PUBLIC_KEY` as a canonical base64 Ed25519 public PEM, require the installer to embed that same key, and run `make build-all VERSION=X.Y.Z RELEASE_PUBLIC_KEY=...` in a read-only-token job.
6. Verify the exact six updater asset names, embedded version, independently expected updater key and its presence in every real artifact, and `SHA256SUMS`, then upload an unsigned artifact.
7. After required approval of the `release-signing` Environment, use a separate job with no source checkout or `GITHUB_TOKEN` permissions to revalidate the exact artifact, checksums, and embedded public key, require its Environment secret `AWG_RELEASE_SIGNING_PRIVATE_KEY` and the configured public key to be one matching Ed25519 pair, sign only `SHA256SUMS`, and verify the signature.
8. Transfer the signed bundle to a separate publish job that alone receives `contents: write`.
9. Create or validate an annotated `vX.Y.Z` tag pointing to the release commit.
10. Publish a non-draft, non-prerelease release and verify its complete description and every published byte against the bundle.

Expected release assets:

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

The workflow uses an annotated tag so recovery can verify a tag object's exact name, message, type, and target commit. Legacy lightweight tags are not eligible. A recovery accepts only the expected annotated tag and either no release or the latest stable release whose complete description and asset set match the newly verified bundle.

The `release-signing` Environment must be restricted to `main` and require a reviewer who inspects the exact workflow diff before granting access to the private key. Same-repository YAML still controls the approved signing step; fully unattended cryptographic isolation requires a separately administered signer or KMS and is outside this repository.

The workflow stops after GitHub Release publication and verification. It must not contain server SSH credentials, a production Environment, or a deployment job. Operators install releases separately or use the signed self-updater.

Official Linux and macOS release binaries embed the same public key and use it for `awg-server update`. Self-update is strictly upgrade-only and validates the canonical version-bound asset URLs, interprocess update lock, actual on-disk version, Ed25519 signature, canonical six-asset manifest, checksum, 64 MiB limit, and downloaded binary version before replacement. Ordinary source builds and Windows self-update fail closed before network access. Key rotation requires the reviewed bridge process documented in `docs/ci-cd.md`; never replace only one key value.

## Manual Release Fallback

Use a manual release only when the user explicitly requests it and GitHub Actions cannot be recovered. Preconditions:

- branch is `main`;
- working tree is clean;
- all automated verification commands pass;
- the version is newer than the latest stable release;
- the changelog section exists;
- neither the tag nor release exists, unless recovering the exact annotated tag and byte-identical release described above;
- an authorized offline copy of the Ed25519 release signing key is available. Never publish an unsigned fallback.

Build and verify:

```bash
set -Eeuo pipefail
umask 077
VERSION=X.Y.Z
PREVIOUS_TAG=vA.B.C
bash scripts/release-notes.sh \
  "$VERSION" CHANGELOG.md "$PREVIOUS_TAG" StealthSurf-VPN/awg-server \
  > /tmp/release-notes.md
test -n "${AWG_RELEASE_SIGNING_PRIVATE_KEY_FILE:-}"
RELEASE_SIGNING_PUBLIC_KEY=$(mktemp)
trap 'rm -f "$RELEASE_SIGNING_PUBLIC_KEY"' EXIT
openssl pkey \
  -in "$AWG_RELEASE_SIGNING_PRIVATE_KEY_FILE" \
  -pubout \
  -out "$RELEASE_SIGNING_PUBLIC_KEY"
KEY_DESCRIPTION=$(openssl pkey -pubin -in "$RELEASE_SIGNING_PUBLIC_KEY" -text -noout)
KEY_TYPE=${KEY_DESCRIPTION%%$'\n'*}
unset KEY_DESCRIPTION
test "$KEY_TYPE" = 'ED25519 Public-Key:'
RELEASE_PUBLIC_KEY=$(openssl base64 -A -in "$RELEASE_SIGNING_PUBLIC_KEY")
make build-all VERSION="$VERSION" RELEASE_PUBLIC_KEY="$RELEASE_PUBLIC_KEY"
AWG_EXPECTED_RELEASE_PUBLIC_KEY="$RELEASE_PUBLIC_KEY" go test -count=1 \
  -run '^TestEmbeddedReleasePublicKey$' \
  -ldflags="-X github.com/stealthsurf-vpn/awg-server/internal/update.releasePublicKey=$RELEASE_PUBLIC_KEY" \
  ./internal/update
test "$(find dist -maxdepth 1 -type f -name 'awg-server-*' | wc -l | tr -d '[:space:]')" -eq 6
HOST_OS=$(go env GOOS)
HOST_ARCH=$(go env GOARCH)
HOST_EXT=
[ "$HOST_OS" != windows ] || HOST_EXT=.exe
test "$("dist/awg-server-$HOST_OS-$HOST_ARCH$HOST_EXT" version)" = "awg-server $VERSION"
ASSETS=(
  awg-server-darwin-amd64
  awg-server-darwin-arm64
  awg-server-linux-amd64
  awg-server-linux-arm64
  awg-server-windows-amd64.exe
  awg-server-windows-arm64.exe
)
for ASSET in "${ASSETS[@]}"; do
  grep -aFq -- "$RELEASE_PUBLIC_KEY" "dist/$ASSET"
done
(
  cd dist
  if command -v sha256sum >/dev/null; then
    sha256sum "${ASSETS[@]}" > SHA256SUMS
    sha256sum --check --strict SHA256SUMS
  else
    shasum -a 256 "${ASSETS[@]}" > SHA256SUMS
    shasum -a 256 --check SHA256SUMS
  fi
)
openssl pkeyutl -sign -rawin \
  -inkey "$AWG_RELEASE_SIGNING_PRIVATE_KEY_FILE" \
  -in dist/SHA256SUMS \
  -out dist/SHA256SUMS.sig
test "$(wc -c < dist/SHA256SUMS.sig | tr -d '[:space:]')" -eq 64
openssl pkeyutl -verify -rawin -pubin \
  -inkey "$RELEASE_SIGNING_PUBLIC_KEY" \
  -sigfile dist/SHA256SUMS.sig \
  -in dist/SHA256SUMS
rm -f "$RELEASE_SIGNING_PUBLIC_KEY"
trap - EXIT
```

Set `PREVIOUS_TAG` to the latest published stable tag. Create and push an annotated tag, then publish `/tmp/release-notes.md`, all binaries, `SHA256SUMS`, and `SHA256SUMS.sig`. Never create a lightweight tag, omit an asset or signature, publish a version older than the current latest release, or publish from an unclean tree.

## Common Failures

- A marker without a changelog section fails packaging.
- A duplicate or lower version fails before tag creation.
- Conflicting marker versions fail release detection.
- A lightweight or mismatched existing tag fails publication.
- Missing or extra release assets fail both package and post-publication verification.
- A missing, non-Ed25519, or mismatched signing secret/public variable fails build or signing before publication.
- A stale previous stable tag fails publication before tag creation.
- A published description that differs from the prepared changelog and compare range fails post-publication verification.
