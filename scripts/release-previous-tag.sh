#!/usr/bin/env bash
set -Eeuo pipefail

export LC_ALL=C

target=${1:-}

fail() {
    printf 'release-previous-tag: %s\n' "$1" >&2
    exit 1
}

[ "$#" -eq 1 ] || fail 'expected exactly one target tag argument'
[[ $target =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] \
    || fail 'target tag must use vMAJOR.MINOR.PATCH without leading zeroes'

semver_less() {
    local left=${1#v}
    local right=${2#v}
    local index
    local left_parts
    local right_parts

    IFS=. read -r -a left_parts <<< "$left"
    IFS=. read -r -a right_parts <<< "$right"

    for index in 0 1 2; do
        if [ "${#left_parts[index]}" -lt "${#right_parts[index]}" ]; then
            return 0
        fi
        if [ "${#left_parts[index]}" -gt "${#right_parts[index]}" ]; then
            return 1
        fi
        if [[ ${left_parts[index]} < ${right_parts[index]} ]]; then
            return 0
        fi
        if [[ ${left_parts[index]} > ${right_parts[index]} ]]; then
            return 1
        fi
    done

    return 1
}

latest=''
previous=''
target_exists=false

while IFS= read -r candidate || [ -n "$candidate" ]; do
    if [[ ! $candidate =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
        continue
    fi

    if [ -z "$latest" ] || semver_less "$latest" "$candidate"; then
        latest=$candidate
    fi

    if [ "$candidate" = "$target" ]; then
        target_exists=true
    elif [ -z "$previous" ] || semver_less "$previous" "$candidate"; then
        previous=$candidate
    fi
done

if [ "$target_exists" = true ]; then
    [ "$latest" = "$target" ] \
        || fail "$target already exists but is not the latest stable release"
    [ -z "$previous" ] || printf '%s\n' "$previous"
    exit 0
fi

if [ -n "$latest" ] && ! semver_less "$latest" "$target"; then
    fail "$target must be newer than $latest"
fi

[ -z "$latest" ] || printf '%s\n' "$latest"
