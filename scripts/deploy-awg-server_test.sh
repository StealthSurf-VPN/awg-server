#!/usr/bin/env bash
set -Eeuo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
image=${AWG_DEPLOY_TEST_IMAGE:-debian:bookworm-slim@sha256:7b140f374b289a7c2befc338f42ebe6441b7ea838a042bbd5acbfca6ec875818}

[ -f "$script_dir/deploy-awg-server" ] || {
    printf 'deploy-awg-server test failed: helper is missing\n' >&2
    exit 1
}

docker run --rm -i \
    -v "$script_dir/deploy-awg-server:/usr/local/sbin/deploy-awg-server:ro" \
    "$image" bash -s <<'CONTAINER'
set -Eeuo pipefail

export DEBIAN_FRONTEND=noninteractive
apt-get update >/dev/null
apt-get install -y --no-install-recommends openssl util-linux >/dev/null
rm -rf /var/lib/apt/lists/*

fail() {
    printf 'deploy-awg-server test failed: %s\n' "$1" >&2
    exit 1
}

write_binary() {
    local path=$1
    local version=$2

    printf '#!/bin/sh\n[ "$1" = version ] || exit 2\nprintf "awg-server %s\\n"\n' "$version" > "$path"
    chmod 0755 "$path"
}

host_asset() {
    case "$(uname -m)" in
        x86_64) printf 'awg-server-linux-amd64\n' ;;
        aarch64|arm64) printf 'awg-server-linux-arm64\n' ;;
        *) fail "unsupported test architecture $(uname -m)" ;;
    esac
}

create_signed_manifest() {
    local binary_path=$1
    local selected_asset=$2
    local signing_key=${3:-/tmp/release-signing-private.pem}
    local asset
    local digest

    digest=$(sha256sum "$binary_path" | cut -d ' ' -f 1)
    : > /tmp/SHA256SUMS
    for asset in \
        awg-server-darwin-amd64 \
        awg-server-darwin-arm64 \
        awg-server-linux-amd64 \
        awg-server-linux-arm64 \
        awg-server-windows-amd64.exe \
        awg-server-windows-arm64.exe; do
        if [ "$asset" = "$selected_asset" ]; then
            printf '%s  %s\n' "$digest" "$asset"
        else
            printf '%064d  %s\n' 0 "$asset"
        fi
    done > /tmp/SHA256SUMS

    openssl pkeyutl -sign -rawin \
        -inkey "$signing_key" \
        -in /tmp/SHA256SUMS \
        -out /tmp/SHA256SUMS.sig
}

send_release() {
    local version=$1
    local binary_path=$2
    local asset=${3:-$(host_asset)}
    local signing_key=${4:-/tmp/release-signing-private.pem}

    create_signed_manifest "$binary_path" "$asset" "$signing_key"
    {
        printf '%s\n' "$version"
        printf '%s\n' "$asset"
        base64 --wrap=0 /tmp/SHA256SUMS.sig
        printf '\n'
        cat /tmp/SHA256SUMS
        cat "$binary_path"
    } | /usr/local/sbin/deploy-awg-server
}

assert_installed_version() {
    local expected=$1
    local actual

    actual=$(/usr/local/bin/awg-server version)
    [ "$actual" = "awg-server $expected" ] \
        || fail "installed version is '$actual', want 'awg-server $expected'"
}

assert_no_temporary_files() {
    if find /usr/local/bin -maxdepth 1 \
        \( -name '.awg-server.incoming.*' \
            -o -name '.awg-server.rollback.*' \
            -o -name '.awg-server.manifest.*' \
            -o -name '.awg-server.signature.*' \) \
        -print -quit | grep -q .; then
        fail 'temporary deployment files remain'
    fi
}

assert_restarts() {
    local expected=$1
    local actual=''

    if [ -f /tmp/restarts ]; then
        actual=$(cat /tmp/restarts)
    fi
    [ "$actual" = "$expected" ] \
        || fail "restart sequence is '$actual', want '$expected'"
}

mkdir -p /test-real /usr/local/bin /etc/awg-server
openssl genpkey -algorithm ED25519 -out /tmp/release-signing-private.pem
openssl pkey -in /tmp/release-signing-private.pem -pubout \
    -out /tmp/release-signing-public.pem
cp /tmp/release-signing-public.pem /etc/awg-server/release-signing-public.pem
openssl genpkey -algorithm ED25519 -out /tmp/untrusted-private.pem
openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:512 \
    -out /tmp/rsa-private.pem 2>/dev/null
openssl pkey -in /tmp/rsa-private.pem -pubout -out /tmp/rsa-public.pem
chmod 0644 /etc/awg-server/release-signing-public.pem
cp /usr/bin/mv /test-real/mv

cat > /usr/bin/systemctl <<'SCRIPT'
#!/bin/sh
set -eu

[ "$1" = restart ]
[ "$2" = awg-server ]

version=$(/usr/local/bin/awg-server version)
printf '%s\n' "$version" >> /tmp/restarts

if [ -f /tmp/interrupt-restart-version ] \
    && grep -Fqx "$version" /tmp/interrupt-restart-version; then
    kill -TERM "$PPID"
    exit 1
fi
if [ -f /tmp/fail-restart-version ] \
    && grep -Fqx "$version" /tmp/fail-restart-version; then
    exit 1
fi
SCRIPT

cat > /usr/bin/curl <<'SCRIPT'
#!/bin/sh
set -eu

version=$(/usr/local/bin/awg-server version)
if [ -f /tmp/fail-health-version ] \
    && grep -Fqx "$version" /tmp/fail-health-version; then
    exit 1
fi

exit 0
SCRIPT

cat > /usr/bin/sleep <<'SCRIPT'
#!/bin/sh
exit 0
SCRIPT

cat > /usr/bin/mv <<'SCRIPT'
#!/bin/sh
set -eu

if [ -f /tmp/fail-rollback-restore ] \
    && [ "$1" = -f ] \
    && [ "$2" = -- ] \
    && printf '%s\n' "$3" | grep -Eq '/\.awg-server\.rollback\.[^/]+$'; then
    exit 1
fi

exec /test-real/mv "$@"
SCRIPT

chmod 0755 /usr/bin/systemctl /usr/bin/curl /usr/bin/sleep /usr/bin/mv

write_binary /usr/local/bin/awg-server 1.0.0
write_binary /tmp/awg-server-new 1.1.0
: > /tmp/restarts
output=$(send_release 1.1.0 /tmp/awg-server-new)
[ "$output" = 1.1.0 ] || fail "successful deploy output is '$output'"
assert_installed_version 1.1.0
assert_restarts 'awg-server 1.1.0'
assert_no_temporary_files

write_binary /tmp/locked-binary 1.2.0
: > /tmp/restarts
exec 8>/usr/local/bin/awg-server.update.lock
flock -n 8
if send_release 1.2.0 /tmp/locked-binary >/tmp/lock-output 2>/tmp/lock-error; then
    fail 'concurrent deployment returned success'
fi
flock -u 8
exec 8>&-
grep -Fq 'already in progress' /tmp/lock-error \
    || fail 'concurrent deployment did not report the interprocess lock'
assert_installed_version 1.1.0
assert_restarts ''
assert_no_temporary_files

write_binary /tmp/untrusted-binary 1.2.0
sed -i '2i touch /tmp/untrusted-binary-executed' /tmp/untrusted-binary
: > /tmp/restarts
if send_release 1.2.0 /tmp/untrusted-binary "$(host_asset)" /tmp/untrusted-private.pem \
    >/tmp/signature-output 2>/tmp/signature-error; then
    fail 'untrusted signature returned success'
fi
[ ! -e /tmp/untrusted-binary-executed ] \
    || fail 'untrusted binary executed before signature verification'
assert_installed_version 1.1.0
assert_restarts ''
assert_no_temporary_files

write_binary /tmp/rsa-signed-binary 1.2.0
sed -i '2i touch /tmp/rsa-signed-binary-executed' /tmp/rsa-signed-binary
cp /tmp/rsa-public.pem /etc/awg-server/release-signing-public.pem
chmod 0644 /etc/awg-server/release-signing-public.pem
: > /tmp/restarts
if send_release 1.2.0 /tmp/rsa-signed-binary "$(host_asset)" /tmp/rsa-private.pem \
    >/tmp/rsa-output 2>/tmp/rsa-error; then
    fail 'RSA signing key returned success'
fi
grep -Fq 'must be Ed25519' /tmp/rsa-error \
    || fail 'RSA public key rejection did not report the key type requirement'
[ ! -e /tmp/rsa-signed-binary-executed ] \
    || fail 'RSA-signed binary executed before key type rejection'
assert_installed_version 1.1.0
assert_restarts ''
assert_no_temporary_files
cp /tmp/release-signing-public.pem /etc/awg-server/release-signing-public.pem
chmod 0644 /etc/awg-server/release-signing-public.pem

write_binary /tmp/awg-server-new 1.2.0
case "$(host_asset)" in
    awg-server-linux-amd64) wrong_asset=awg-server-linux-arm64 ;;
    *) wrong_asset=awg-server-linux-amd64 ;;
esac
: > /tmp/restarts
if send_release 1.2.0 /tmp/awg-server-new "$wrong_asset" \
    >/tmp/architecture-output 2>/tmp/architecture-error; then
    fail 'wrong architecture returned success'
fi
assert_installed_version 1.1.0
assert_restarts ''
assert_no_temporary_files

write_binary /tmp/awg-server-new 1.2.0
: > /tmp/restarts
if send_release 1.2.1 /tmp/awg-server-new >/tmp/mismatch-output 2>/tmp/mismatch-error; then
    fail 'version mismatch returned success'
fi
assert_installed_version 1.1.0
assert_restarts ''
assert_no_temporary_files

write_binary /tmp/awg-server-new 1.0.9
: > /tmp/restarts
if send_release 1.0.9 /tmp/awg-server-new >/tmp/downgrade-output 2>/tmp/downgrade-error; then
    fail 'downgrade returned success'
fi
assert_installed_version 1.1.0
assert_restarts ''
assert_no_temporary_files

write_binary /tmp/awg-server-new 2.0.0
printf 'awg-server 2.0.0\n' > /tmp/fail-health-version
: > /tmp/restarts
if send_release 2.0.0 /tmp/awg-server-new >/tmp/health-output 2>/tmp/health-error; then
    fail 'failed health check returned success'
fi
rm -f /tmp/fail-health-version
assert_installed_version 1.1.0
assert_restarts $'awg-server 2.0.0\nawg-server 1.1.0'
assert_no_temporary_files

write_binary /tmp/awg-server-new 2.1.0
printf 'awg-server 2.1.0\n' > /tmp/fail-restart-version
: > /tmp/restarts
if send_release 2.1.0 /tmp/awg-server-new >/tmp/restart-output 2>/tmp/restart-error; then
    fail 'failed service restart returned success'
fi
rm -f /tmp/fail-restart-version
assert_installed_version 1.1.0
assert_restarts $'awg-server 2.1.0\nawg-server 1.1.0'
assert_no_temporary_files

write_binary /tmp/awg-server-new 2.2.0
printf 'awg-server 2.2.0\n' > /tmp/interrupt-restart-version
: > /tmp/restarts
set +e
send_release 2.2.0 /tmp/awg-server-new >/tmp/interrupt-output 2>/tmp/interrupt-error
interrupt_status=$?
set -e
rm -f /tmp/interrupt-restart-version
[ "$interrupt_status" -eq 143 ] \
    || fail "interrupted deployment exited $interrupt_status, want 143"
assert_installed_version 1.1.0
assert_restarts $'awg-server 2.2.0\nawg-server 1.1.0'
assert_no_temporary_files

write_binary /tmp/awg-server-new 3.0.0
printf 'awg-server 3.0.0\n' > /tmp/fail-health-version
touch /tmp/fail-rollback-restore
: > /tmp/restarts
if send_release 3.0.0 /tmp/awg-server-new >/tmp/restore-output 2>/tmp/restore-error; then
    fail 'failed rollback restore returned success'
fi
rm -f /tmp/fail-health-version /tmp/fail-rollback-restore

rollback_backup=$(find /usr/local/bin -maxdepth 1 -type f \
    -name '.awg-server.rollback.*' -print -quit)
[ -n "$rollback_backup" ] \
    || fail 'failed rollback restore deleted the recovery binary'
[ "$("$rollback_backup" version)" = 'awg-server 1.1.0' ] \
    || fail 'preserved recovery binary has the wrong version'
grep -Fq "recovery binary preserved at $rollback_backup" /tmp/restore-error \
    || fail 'failed rollback restore did not report the recovery path'
assert_restarts 'awg-server 3.0.0'
rm -f -- "$rollback_backup"

printf 'deploy-awg-server tests passed\n'
CONTAINER
