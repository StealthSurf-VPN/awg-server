#!/usr/bin/env bash
set -Eeuo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
parser="$script_dir/release-marker.sh"

fail() {
    printf 'release-marker test failed: %s\n' "$1" >&2
    exit 1
}

assert_marker() {
    local name=$1
    local expected=$2
    local input=$3
    local actual

    actual=$(printf '%s' "$input" | "$parser") \
        || fail "$name returned a non-zero status"

    [ "$actual" = "$expected" ] \
        || fail "$name: expected '$expected', got '$actual'"
}

assert_conflict() {
    local input=$1
    local stderr_file

    stderr_file=$(mktemp)
    trap 'rm -f "$stderr_file"' RETURN

    if printf '%s' "$input" | "$parser" >/dev/null 2>"$stderr_file"; then
        fail 'conflicting markers returned success'
    fi

    grep -Fq 'multiple release markers found' "$stderr_file" \
        || fail 'conflicting markers did not explain the failure'
}

assert_marker 'no marker' '' 'feat: update client settings'
assert_marker 'commit marker' 'v1.2.3' 'feat: update client settings release:v1.2.3'
assert_marker 'multiline message marker' 'v2.0.0' $'Commit title\n\nRelease notes (release:v2.0.0).'
assert_marker 'zero version' 'v0.0.0' 'release:v0.0.0'
assert_marker 'duplicate identical markers' 'v3.4.5' $'release:v3.4.5\nrelease:v3.4.5'
assert_marker 'marker punctuation boundary' 'v4.5.6' '[release:v4.5.6], ready'
assert_marker 'leading zero rejected' '' 'release:v01.2.3'
assert_marker 'prerelease rejected' '' 'release:v1.2.3-rc.1'
assert_marker 'build metadata rejected' '' 'release:v1.2.3+build.4'
assert_marker 'extra version segment rejected' '' 'release:v1.2.3.4'
assert_marker 'embedded prefix rejected' '' 'notrelease:v1.2.3'
assert_marker 'embedded suffix rejected' '' 'release:v1.2.3beta'
assert_conflict $'release:v1.2.3\nrelease:v2.0.0'

printf 'release-marker tests passed\n'
