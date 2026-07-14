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
    actual=$("$parser" 2.0.0 "$temp_dir/CHANGELOG.md") \
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

assert_notes 'dated section' $'- Added signed releases.\n\nDetails.' $'# Changelog\n\n## [2.0.0] - 2026-07-15\n\n- Added signed releases.\n\nDetails.\n\n## [1.0.0] - 2026-01-01\n\n- Initial.'
assert_notes 'ignores invalid same-version heading' '- Correct dated notes.' $'# Changelog\n\n## [2.0.0] - TBD\n\n- Wrong notes.\n\n## [2.0.0] - 2026-07-15\n\n- Correct dated notes.\n\n## [1.0.0] - 2026-01-01\n'
assert_failure 'missing dated section' $'# Changelog\n\n## [2.0.0] - TBD\n\n- Notes.'
assert_failure 'duplicate dated section' $'## [2.0.0] - 2026-07-15\n\n- First.\n\n## [2.0.0] - 2026-07-16\n\n- Second.'
assert_failure 'empty dated section' $'## [2.0.0] - 2026-07-15\n\n  \n## [1.0.0] - 2026-01-01\n\n- Old.'
assert_failure 'does not borrow invalid heading notes' $'## [2.0.0] - TBD\n\n- Wrong notes.\n\n## [2.0.0] - 2026-07-15\n\n  \n## [1.0.0] - 2026-01-01\n'

printf '# Changelog\n' > "$temp_dir/CHANGELOG.md"
if "$parser" v2.0.0 "$temp_dir/CHANGELOG.md" >/dev/null 2>"$temp_dir/error"; then
    fail 'invalid version returned success'
fi

printf 'release-notes tests passed\n'
