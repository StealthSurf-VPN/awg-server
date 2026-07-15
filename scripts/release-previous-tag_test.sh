#!/usr/bin/env bash
set -Eeuo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
resolver="$script_dir/release-previous-tag.sh"

fail() {
    printf 'release-previous-tag test failed: %s\n' "$1" >&2
    exit 1
}

assert_previous() {
    local name=$1
    local expected=$2
    local target=$3
    local releases=$4
    local actual

    actual=$(printf '%s' "$releases" | "$resolver" "$target") \
        || fail "$name returned a non-zero status"
    [ "$actual" = "$expected" ] \
        || fail "$name: expected '$expected', got '$actual'"
}

assert_failure() {
    local name=$1
    local target=$2
    local releases=$3

    if printf '%s' "$releases" | "$resolver" "$target" >/dev/null 2>/dev/null; then
        fail "$name returned success"
    fi
}

assert_previous 'first release' '' v1.0.0 ''
assert_previous 'new release' v1.0.3 v1.0.4 $'v1.0.1\nv1.0.3\nv1.0.2\n'
assert_previous 'release recovery' v1.0.3 v1.0.4 $'v1.0.4\nv1.0.2\nv1.0.3\n'
assert_previous 'ignores nonstable tags' v1.0.3 v1.0.4 $'v1.0.3\nv2.0.0-rc.1\ninvalid\n'
assert_previous 'arbitrary length components' v999999999999999999999.0.0 v1000000000000000000000.0.0 $'v999999999999999999999.0.0\n'
assert_failure 'lower release' v1.0.2 $'v1.0.3\n'
assert_failure 'existing release is not latest' v1.0.3 $'v1.0.3\nv1.0.4\n'
assert_failure 'invalid target' v01.0.0 $'v1.0.0\n'

printf 'release-previous-tag tests passed\n'
