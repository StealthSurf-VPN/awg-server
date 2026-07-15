#!/usr/bin/env bash
set -Eeuo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
parser="$script_dir/release-notes.sh"
temp_dir=$(mktemp -d)
trap 'rm -rf "$temp_dir"' EXIT

fail() {
    printf 'release-notes test failed: %s\n' "$1" >&2
    exit 1
}

assert_notes() {
    local name=$1
    local expected=$2
    local fixture=$3
    local actual

    printf '%s' "$fixture" > "$temp_dir/CHANGELOG.md"
    actual=$("$parser" 2.0.0 "$temp_dir/CHANGELOG.md" v1.0.0 StealthSurf-VPN/awg-server) \
        || fail "$name returned a non-zero status"
    [ "$actual" = "$expected" ] \
        || fail "$name: expected '$expected', got '$actual'"
}

assert_failure() {
    local name=$1
    local fixture=$2

    printf '%s' "$fixture" > "$temp_dir/CHANGELOG.md"
    if "$parser" 2.0.0 "$temp_dir/CHANGELOG.md" >/dev/null 2>"$temp_dir/error"; then
        fail "$name returned success"
    fi
}

assert_argument_failure() {
    local name=$1
    local previous_tag=$2
    local repository=$3

    printf '%s' $'## [2.0.0] - 2026-07-15\n\n- Notes.\n' > "$temp_dir/CHANGELOG.md"
    if "$parser" 2.0.0 "$temp_dir/CHANGELOG.md" "$previous_tag" "$repository" \
        >/dev/null 2>"$temp_dir/error"; then
        fail "$name returned success"
    fi
}

assert_notes 'dated section' $'- Added signed releases.\n\nDetails.\n\n**Full Changelog**: [v1.0.0...v2.0.0](https://github.com/StealthSurf-VPN/awg-server/compare/v1.0.0...v2.0.0)' $'# Changelog\n\n## [2.0.0] - 2026-07-15\n\n- Added signed releases.\n\nDetails.\n\n## [1.0.0] - 2026-01-01\n\n- Initial.'
assert_notes 'ignores invalid same-version heading' $'- Correct dated notes.\n\n**Full Changelog**: [v1.0.0...v2.0.0](https://github.com/StealthSurf-VPN/awg-server/compare/v1.0.0...v2.0.0)' $'# Changelog\n\n## [2.0.0] - TBD\n\n- Wrong notes.\n\n## [2.0.0] - 2026-07-15\n\n- Correct dated notes.\n\n## [1.0.0] - 2026-01-01\n'
assert_failure 'missing dated section' $'# Changelog\n\n## [2.0.0] - TBD\n\n- Notes.'
assert_failure 'duplicate dated section' $'## [2.0.0] - 2026-07-15\n\n- First.\n\n## [2.0.0] - 2026-07-16\n\n- Second.'
assert_failure 'empty dated section' $'## [2.0.0] - 2026-07-15\n\n  \n## [1.0.0] - 2026-01-01\n\n- Old.'
assert_failure 'does not borrow invalid heading notes' $'## [2.0.0] - TBD\n\n- Wrong notes.\n\n## [2.0.0] - 2026-07-15\n\n  \n## [1.0.0] - 2026-01-01\n'
assert_argument_failure 'invalid previous tag' v01.0.0 StealthSurf-VPN/awg-server
assert_argument_failure 'invalid repository' v1.0.0 StealthSurf-VPN/awg-server/extra
assert_argument_failure 'repository without previous tag' '' StealthSurf-VPN/awg-server

printf '%s' $'## [2.0.0] - 2026-07-15\n\n- Initial release.\n' > "$temp_dir/CHANGELOG.md"
initial_notes=$("$parser" 2.0.0 "$temp_dir/CHANGELOG.md") \
    || fail 'initial release returned a non-zero status'
[ "$initial_notes" = '- Initial release.' ] \
    || fail "initial release notes are '$initial_notes'"

printf '# Changelog\n' > "$temp_dir/CHANGELOG.md"
if "$parser" v2.0.0 "$temp_dir/CHANGELOG.md" >/dev/null 2>"$temp_dir/error"; then
    fail 'invalid version returned success'
fi

printf 'release-notes tests passed\n'
