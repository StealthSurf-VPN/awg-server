#!/usr/bin/env bash
set -Eeuo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
installer="$script_dir/install.sh"

fail() {
    printf 'install test failed: %s\n' "$1" >&2
    exit 1
}

[[ -f $installer ]] || fail 'scripts/install.sh is missing'
# shellcheck source=scripts/install.sh
source "$installer"

temp_dir=$(mktemp -d)
trap 'rm -rf "$temp_dir"' EXIT

assert_equal() {
    local name=$1
    local expected=$2
    local actual=$3

    [[ $actual == "$expected" ]] \
        || fail "$name: expected '$expected', got '$actual'"
}

assert_rejected() {
    local name=$1
    shift

    if ("$@") >"$temp_dir/stdout" 2>"$temp_dir/stderr"; then
        fail "$name returned success"
    fi
}

clear_config() {
    local key

    for key in "${CONFIG_KEYS[@]}"; do
        unset "$key"
    done
}

validate_version 0.0.0 || fail 'zero stable version was rejected'
validate_version 12.34.56 || fail 'stable version was rejected'
for version in latest v1.2.3 01.2.3 1.2.3-rc.1 1.2; do
    assert_rejected "invalid version $version" validate_version "$version"
done

assert_equal 'x86_64 release asset' \
    awg-server-linux-amd64 "$(release_asset_name x86_64)"
assert_equal 'aarch64 release asset' \
    awg-server-linux-arm64 "$(release_asset_name aarch64)"
assert_equal 'arm64 release asset' \
    awg-server-linux-arm64 "$(release_asset_name arm64)"
assert_rejected 'unsupported release architecture' release_asset_name riscv64

health_response_ok '{"status":"ok"}' \
    || fail 'exact health response was rejected'
for response in '{"status":"degraded"}' '{"status":"ok"} ' ''; do
    assert_rejected "invalid health response $response" health_response_ok "$response"
done

invocation_changed invocation-old invocation-new \
    || fail 'changed service invocation was rejected'
assert_rejected 'unchanged service invocation' \
    invocation_changed invocation-same invocation-same
assert_rejected 'empty current service invocation' \
    invocation_changed invocation-old ''

caller_exit_trap=$(trap -p EXIT)
registered_temp_path="$temp_dir/registered-temp"
mkdir "$registered_temp_path"
INSTALL_TEMP_PATHS=("$registered_temp_path")
cleanup_temp_paths || fail 'registered temporary paths were not cleaned'
[[ ! -e $registered_temp_path ]] \
    || fail 'registered temporary path still exists after cleanup'
assert_equal 'caller EXIT trap preservation' \
    "$caller_exit_trap" "$(trap -p EXIT)"
INSTALL_TEMP_PATHS=()
if ! (
    INSTALL_TEMP_PATHS=()
    cleanup_temp_paths
); then
    fail 'empty temporary-path registry was rejected'
fi
if grep -Eq 'trap[[:space:]]+-[[:space:]]+EXIT' "$installer"; then
    fail 'installer helpers clear the caller EXIT trap'
fi

existing_config="$temp_dir/existing.env"
explicit_config="$temp_dir/explicit.env"
printf '%s\n' \
    'AWG_API_TOKEN=existing-token' \
    'AWG_ADDRESS=10.0.0.1/24' \
    'AWG_ENDPOINT=existing.example.com' \
    'AWG_RELEASE_PUBLIC_KEY_FILE=/existing/release-signing-public.pem' >"$existing_config"
printf '%s\n' \
    'AWG_API_TOKEN=explicit-token' \
    'AWG_ADDRESS=10.1.0.1/24' \
    'AWG_RELEASE_PUBLIC_KEY_FILE=/explicit/release-signing-public.pem' >"$explicit_config"
chmod 0600 "$existing_config" "$explicit_config"

# Config precedence cases intentionally isolate their environment changes.
# shellcheck disable=SC2030,SC2031
(
    clear_config
    AWG_API_TOKEN=process-token
    AWG_RELEASE_PUBLIC_KEY_FILE=/process/release-signing-public.pem
    capture_process_environment
    load_config_file "$existing_config"
    load_config_file "$explicit_config"
    restore_process_environment

    assert_equal 'process environment precedence' process-token "$AWG_API_TOKEN"
    assert_equal 'release key process environment precedence' \
        /process/release-signing-public.pem "$AWG_RELEASE_PUBLIC_KEY_FILE"
    assert_equal 'explicit config precedence' 10.1.0.1/24 "$AWG_ADDRESS"
    assert_equal 'existing config fallback' existing.example.com "$AWG_ENDPOINT"
)
# Config precedence cases intentionally isolate their environment changes.
# shellcheck disable=SC2030,SC2031
(
    clear_config
    unset AWG_RELEASE_PUBLIC_KEY_FILE
    capture_process_environment
    load_config_file "$existing_config"
    load_config_file "$explicit_config"
    restore_process_environment

    assert_equal 'release key explicit config precedence' \
        /explicit/release-signing-public.pem "$AWG_RELEASE_PUBLIC_KEY_FILE"
)

if (
    clear_config
    require_setting AWG_API_TOKEN </dev/null
) >"$temp_dir/stdout" 2>"$temp_dir/stderr"; then
    fail 'missing non-interactive setting returned success'
fi
grep -Fq 'AWG_API_TOKEN is required' "$temp_dir/stderr" \
    || fail 'missing non-interactive setting did not explain the failure'

if (
    unset AWG_RELEASE_PUBLIC_KEY_FILE
    require_setting AWG_RELEASE_PUBLIC_KEY_FILE </dev/null
) >"$temp_dir/stdout" 2>"$temp_dir/stderr"; then
    fail 'missing non-interactive release key setting returned success'
fi
grep -Fq 'AWG_RELEASE_PUBLIC_KEY_FILE is required' "$temp_dir/stderr" \
    || fail 'missing release key setting did not explain the failure'

release_key_required=false
for key in "${REQUIRED_KEYS[@]}"; do
    [[ $key != AWG_RELEASE_PUBLIC_KEY_FILE ]] || release_key_required=true
done
[[ $release_key_required == true ]] \
    || fail 'AWG_RELEASE_PUBLIC_KEY_FILE is not a required installer setting'

# The read stub is invoked indirectly by prompt_setting from install.sh.
# shellcheck disable=SC2031,SC2317
if ! (
    clear_config
    read() {
        local arg destination
        local secret_flag=false

        for arg in "$@"; do
            case $arg in
                -*s*) secret_flag=true ;;
            esac
        done
        [[ $secret_flag == true ]] || return 64

        destination=${!#}
        printf -v "$destination" '%s' interactive-secret
    }

    prompt_setting AWG_API_TOKEN 'API token'
    assert_equal 'interactive secret assignment' interactive-secret "$AWG_API_TOKEN"
) >"$temp_dir/stdout" 2>"$temp_dir/stderr"; then
    fail 'interactive AWG_API_TOKEN did not use secret read mode'
fi
if grep -Fq interactive-secret "$temp_dir/stdout" "$temp_dir/stderr"; then
    fail 'interactive AWG_API_TOKEN was written to output'
fi

insecure_config="$temp_dir/insecure.env"
printf '%s\n' 'AWG_API_TOKEN=insecure-token' >"$insecure_config"
chmod 0622 "$insecure_config"
assert_rejected 'group/world-writable config' load_config_file "$insecure_config"
assert_rejected 'non-regular config' load_config_file "$temp_dir"

rendered_config="$temp_dir/rendered.env"
# Shell metacharacters are literal serializer input.
# shellcheck disable=SC2016
environment_value='value with spaces $dollar \backslash "quote" `backtick`'
# Expected output intentionally contains literal shell escapes.
# shellcheck disable=SC2016
expected_environment_line='AWG_API_TOKEN="value with spaces \$dollar \\backslash \"quote\" \`backtick\`"'
# Rendering is intentionally isolated from the remaining config tests.
# shellcheck disable=SC2030
(
    clear_config
    AWG_API_TOKEN=$environment_value
    AWG_ADDRESS=10.2.0.1/24
    AWG_ENDPOINT=vpn.example.com
    AWG_RELEASE_PUBLIC_KEY_FILE=/etc/awg-server/release-signing-public.pem
    AWG_I1='<b 0xc0><r 32><t>'
    render_environment >"$rendered_config"
)
grep -q '^AWG_API_TOKEN=' "$rendered_config" \
    || fail 'rendered environment omitted AWG_API_TOKEN'
grep -q '^AWG_I1=' "$rendered_config" \
    || fail 'rendered environment omitted AWG_I1'
assert_equal 'systemd-compatible environment representation' \
    "$expected_environment_line" \
    "$(grep '^AWG_API_TOKEN=' "$rendered_config")"
if grep -q '^AWG_SERVER_VERSION=' "$rendered_config"; then
    fail 'rendered service environment included AWG_SERVER_VERSION'
fi
if grep -q '^AWG_RELEASE_PUBLIC_KEY_FILE=' "$rendered_config"; then
    fail 'rendered service environment included AWG_RELEASE_PUBLIC_KEY_FILE'
fi
# Sourcing the rendered fixture is intentionally isolated from its creation.
# shellcheck disable=SC2031
(
    clear_config
    # shellcheck source=/dev/null
    source "$rendered_config"
    assert_equal 'rendered token round trip' "$environment_value" "$AWG_API_TOKEN"
    assert_equal 'rendered address round trip' 10.2.0.1/24 "$AWG_ADDRESS"
    assert_equal 'rendered CPS round trip' '<b 0xc0><r 32><t>' "$AWG_I1"
)

for separator in $'\n' $'\r'; do
    if (
        clear_config
        AWG_API_TOKEN="before${separator}after"
        render_environment
    ) >"$temp_dir/stdout" 2>"$temp_dir/stderr"; then
        fail 'CR/LF environment value returned success'
    fi
    [[ ! -s $temp_dir/stdout ]] \
        || fail 'CR/LF environment value produced partial output'
    grep -Fq 'must not contain carriage returns or newlines' "$temp_dir/stderr" \
        || fail 'CR/LF environment rejection did not explain the failure'
done

legacy_dir="$temp_dir/data"
default_dir="$temp_dir/var/lib/awg-server"
mkdir -p "$legacy_dir" "$default_dir"
(
    unset AWG_DATA_DIR
    select_data_dir "$legacy_dir" "$default_dir"
    assert_equal 'legacy data directory' "$legacy_dir" "$AWG_DATA_DIR"
)
(
    unset AWG_DATA_DIR
    select_data_dir "$temp_dir/missing-data" "$default_dir"
    assert_equal 'new data directory' "$default_dir" "$AWG_DATA_DIR"
)
(
    AWG_DATA_DIR="$temp_dir/custom"
    select_data_dir "$legacy_dir" "$default_dir"
    assert_equal 'configured data directory' "$temp_dir/custom" "$AWG_DATA_DIR"
)

printf 'install tests passed\n'
