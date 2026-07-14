#!/usr/bin/env bash
set -Eeuo pipefail

export LC_ALL=C

input=$(cat)
remaining=$input
markers=''
pattern='(^|[^[:alnum:]_.-])(release:(v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)))'

while [[ $remaining =~ $pattern ]]; do
    match=${BASH_REMATCH[0]}
    tag=${BASH_REMATCH[3]}
    remaining=${remaining#*"$match"}
    next_character=${remaining:0:1}

    if [[ -z $next_character || ! $next_character =~ [[:alnum:]_.+-] ]]; then
        markers+="$tag"$'\n'
    fi
done

unique_markers=$(printf '%s' "$markers" | sed '/^$/d' | sort -u)
if [ -z "$unique_markers" ]; then
    exit 0
fi

marker_count=$(printf '%s\n' "$unique_markers" | wc -l | tr -d '[:space:]')
if [ "$marker_count" -ne 1 ]; then
    printf 'multiple release markers found:\n%s\n' "$unique_markers" >&2
    exit 1
fi

printf '%s\n' "$unique_markers"
