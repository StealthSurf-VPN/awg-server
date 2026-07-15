#!/usr/bin/env bash
set -Eeuo pipefail

export LC_ALL=C

version=${1:-}
changelog=${2:-CHANGELOG.md}
previous_tag=${3:-}
repository=${4:-}

fail() {
    printf 'release-notes: %s\n' "$1" >&2
    exit 1
}

[[ $version =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] \
    || fail 'version must use MAJOR.MINOR.PATCH without leading zeroes'
[ -r "$changelog" ] || fail "cannot read $changelog"
if [ -n "$previous_tag" ]; then
    [[ $previous_tag =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] \
        || fail 'previous tag must use vMAJOR.MINOR.PATCH without leading zeroes'
    [[ $repository =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]] \
        || fail 'repository must use OWNER/REPOSITORY'
elif [ -n "$repository" ]; then
    fail 'repository requires a previous tag'
fi

escaped_version=${version//./\\.}
set +e
matches=$(grep -nE "^## \\[$escaped_version\\] - [0-9]{4}-[0-9]{2}-[0-9]{2}$" "$changelog")
grep_status=$?
set -e
if [ "$grep_status" -gt 1 ]; then
    fail "failed to search $changelog"
fi
if [ -z "$matches" ]; then
    match_count=0
else
    match_count=$(printf '%s\n' "$matches" | wc -l | tr -d '[:space:]')
fi
[ "$match_count" -eq 1 ] \
    || fail "$changelog must contain exactly one dated section for $version"

heading_line=${matches%%:*}
notes=$(
    awk -v heading_line="$heading_line" '
        NR <= heading_line { next }
        /^## \[/ { exit }
        /^[[:space:]]*$/ {
            if (started) {
                pending_blanks = pending_blanks ORS
            }
            next
        }
        {
            if (pending_blanks != "") {
                printf "%s", pending_blanks
                pending_blanks = ""
            }
            print
            started = 1
        }
    ' "$changelog"
)
printf '%s\n' "$notes" | grep -q '[^[:space:]]' \
    || fail "the dated $version section is empty"

printf '%s\n' "$notes"

if [ -n "$previous_tag" ]; then
    printf '\n**Full Changelog**: [%s...v%s](https://github.com/%s/compare/%s...v%s)\n' \
        "$previous_tag" "$version" "$repository" "$previous_tag" "$version"
fi
