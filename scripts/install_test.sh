#!/usr/bin/env bash
set -Eeuo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
installer="$script_dir/install.sh"
temp_dir=$(mktemp -d)
original_path=$PATH

trap 'rm -rf -- "$temp_dir"' EXIT

fail() {
    printf 'install test failed: %s\n' "$1" >&2
    exit 1
}

assert_file_contains() {
    local file=$1
    local expected=$2

    grep -Fq -- "$expected" "$file" \
        || fail "$file does not contain expected content"
}

assert_file_not_contains() {
    local file=$1
    local unexpected=$2

    if grep -Fq -- "$unexpected" "$file"; then
        fail "$file contains sensitive or unexpected content"
    fi
}

file_mode() {
    local path=$1

    if stat -c '%a' -- "$path" 2>/dev/null; then
        return
    fi

    stat -f '%Lp' "$path"
}

assert_mode() {
    local path=$1
    local expected=$2
    local actual

    actual=$(file_mode "$path") || fail "could not read mode for $path"
    [ "$actual" = "$expected" ] \
        || fail "$path mode is $actual, want $expected"
}

assert_before() {
    local file=$1
    local first=$2
    local second=$3
    local first_line
    local second_line

    first_line=$(grep -n -F -- "$first" "$file" | head -n 1 | cut -d: -f1) \
        || fail "$first is absent from $file"
    second_line=$(grep -n -F -- "$second" "$file" | head -n 1 | cut -d: -f1) \
        || fail "$second is absent from $file"
    [ "$first_line" -lt "$second_line" ] \
        || fail "$first did not precede $second"
}

assert_stopped() {
    local state_file=$1

    [ "$(<"$state_file")" = inactive ] \
        || fail 'service was not left stopped'
}

backup_dir() {
    local root=$1
    local directories

    directories=$(find "$root" -mindepth 1 -maxdepth 1 -type d -print) \
        || fail 'could not find backup directory'
    [ "$(printf '%s\n' "$directories" | sed '/^$/d' | wc -l | tr -d '[:space:]')" = 1 ] \
        || fail 'expected exactly one completed backup directory'
    printf '%s\n' "$directories"
}

reset_settings() {
    local key

    for key in "${ENVIRONMENT_KEYS[@]}"; do
        unset "$key" || true
    done

    AWG_SERVER_VERSION=''
    PROCESS_ENV_PRESENT=()
    PROCESS_ENV_VALUES=()
    LOADED_CONFIG_PRESENT=()
    LOADED_CONFIG_VALUES=()
    INSTALL_TEMP_PATHS=()
    INSTALLER_BACKUP_DIR=''
    INSTALLER_STAGED_BINARY=''
}

write_stubs() {
    local stub_dir=$1
    local command

    tee "$stub_dir/stub-command" >/dev/null <<'STUB'
#!/usr/bin/env bash
set -Eeuo pipefail

name=${0##*/}

log() {
    printf '%s\n' "$*" >> "${STUB_TRACE:?}"
}

mode_for_path() {
    local path=$1

    if stat -c '%a' -- "$path" 2>/dev/null; then
        return
    fi

    stat -f '%Lp' "$path"
}

case "$name" in
    apt-get)
        log "apt-get $*"
        [ "${STUB_FAIL_PHASE:-}" != apt ] || exit 1
        ;;
    add-apt-repository)
        log "add-apt-repository $*"
        [ "${STUB_FAIL_PHASE:-}" != ppa ] || exit 1
        ;;
    awg)
        log "awg $*"
        printf '%s\n' 'amneziawg-tools v3.1.20260828 - https://amnezia.org'
        ;;
    dpkg-query)
        package=${!#}
        case "$package" in
            amneziawg-tools) version=${STUB_TOOLS_VERSION:?} ;;
            amneziawg-dkms) version=${STUB_DKMS_VERSION:?} ;;
            *) exit 1 ;;
        esac
        case "${STUB_PACKAGE_STATUS_MODE:-installed}" in
            installed) ;;
            missing) exit 1 ;;
            malformed)
                printf '%s\n' 'not-a-package-status'
                exit 0
                ;;
            not-installed)
                printf 'rc \t%s\n' "$version"
                exit 0
                ;;
            *) exit 1 ;;
        esac
        case "${STUB_PACKAGE_VERSION_MODE:-exact}" in
            exact) ;;
            newer) version="9.9.9-0~synthetic" ;;
            older) version="0.0.0-0~synthetic" ;;
            *) exit 1 ;;
        esac
        printf 'ii \t%s\n' "$version"
        ;;
    dpkg)
        log "dpkg $*"
        [ "${STUB_FAIL_PHASE:-}" != package-compare ] || exit 1
        [ "${STUB_PACKAGE_VERSION_MODE:-exact}" != older ] || exit 1
        ;;
    curl)
        config_file=''
        output_file=''
        url=''
        arguments=("$@")
        for index in "${!arguments[@]}"; do
            argument=${arguments[index]}
            case "$argument" in
                --config)
                    config_file=${arguments[index + 1]}
                    ;;
                -o)
                    output_file=${arguments[index + 1]}
                    ;;
                http://* | https://*)
                    url=$argument
                    ;;
            esac
        done
        printf '%q ' "$@" >> "${STUB_CURL_ARGUMENTS:?}"
        printf '\n' >> "${STUB_CURL_ARGUMENTS:?}"
        case "$url" in
            'https://github.com/StealthSurf-VPN/awg-server/releases/latest/download/SHA256SUMS')
                log 'curl latest-manifest'
                printf '%s\n' 'HTTP/2 302' \
                    'location: https://github.com/StealthSurf-VPN/awg-server/releases/download/v1.2.3/SHA256SUMS'
                ;;
            */SHA256SUMS)
                log 'curl checksum-manifest'
                printf '%064d  awg-server-linux-amd64\n' 0 > "$output_file"
                ;;
            */SHA256SUMS.sig)
                log 'curl checksum-signature'
                printf 'synthetic-signature' > "$output_file"
                ;;
            */awg-server-linux-amd64)
                log 'curl release-binary'
                printf '%s\n' \
                    '#!/usr/bin/env bash' \
                    'set -Eeuo pipefail' \
                    'case "${1:-}" in' \
                    'version)' \
                    '    printf "awg-server %s\\n" "${STUB_VERSION:?}"' \
                    '    ;;' \
                    'check-runtime)' \
                    '    printf "%s\\n" staged-check-runtime >> "${STUB_TRACE:?}"' \
                    '    if grep -Fqx -- old-binary "${STUB_BINARY_PATH:?}"; then' \
                    '        printf "%s\\n" old-binary-present-at-check >> "${STUB_TRACE:?}"' \
                    '    fi' \
                    '    [ "${STUB_FAIL_PHASE:-}" != check-runtime ] || exit 1' \
                    '    printf "%s\\n" qualified' \
                    '    ;;' \
                    '*) exit 1 ;;' \
                    'esac' > "$output_file"
                chmod 0755 "$output_file"
                ;;
            */health)
                log 'curl health'
                [ "${STUB_FAIL_PHASE:-}" != health ] || exit 1
                printf '%s' "${STUB_HEALTH_RESPONSE:-{\"status\":\"ok\"}}"
                ;;
            */api/clients)
                log 'curl clients'
                [ -n "$config_file" ] || exit 1
                mode=$(mode_for_path "$config_file")
                printf '%s\n' "$mode" > "${STUB_CURL_CONFIG_MODE:?}"
                printf '%s\n' "$config_file" > "${STUB_CURL_CONFIG_PATH:?}"
                grep -Fq -- "Authorization: Bearer ${STUB_EXPECTED_TOKEN:?}" "$config_file" \
                    || exit 1
                case "${STUB_FAIL_PHASE:-}" in
                    clients | auth-transport) exit 1 ;;
                esac
                if [ "${STUB_FAIL_PHASE:-}" = clients-json ]; then
                    printf '%s' '{}'
                else
                    printf '%s' "${STUB_CLIENTS_RESPONSE:-[]}"
                fi
                ;;
            *)
                exit 1
                ;;
        esac
        ;;
    openssl)
        log "openssl $*"
        case " $* " in
            *' base64 -d -A '*) printf 'synthetic-public-key' ;;
            *' pkey -pubin '* ) printf '%s\n' 'ED25519 Public-Key:' ;;
            *' pkeyutl -verify '*)
                [ "${STUB_FAIL_PHASE:-}" != signature ] || exit 1
                ;;
            *) exit 1 ;;
        esac
        ;;
    sha256sum)
        log "sha256sum $*"
        [ "${STUB_FAIL_PHASE:-}" != checksum ] || exit 1
        ;;
    systemctl)
        action=''
        for argument in "$@"; do
            case "$argument" in
                is-active | stop | daemon-reload | enable | start | show | is-enabled)
                    action=$argument
                    break
                    ;;
            esac
        done
        log "systemctl ${action:-unknown}"
        case "$action" in
            is-active)
                if [ -n "${STUB_SYSTEMCTL_STATUS:-}" ]; then
                    exit "$STUB_SYSTEMCTL_STATUS"
                fi
                if [ "$(<"${STUB_STATE_DIR:?}/service-state")" = active ]; then
                    exit 0
                fi
                exit 3
                ;;
            stop)
                stop_count=0
                if [ -e "${STUB_STATE_DIR:?}/stop-count" ]; then
                    stop_count=$(<"${STUB_STATE_DIR:?}/stop-count")
                fi
                stop_count=$((stop_count + 1))
                printf '%s\n' "$stop_count" > "${STUB_STATE_DIR:?}/stop-count"
                if [ "$stop_count" -gt 1 ]; then
                    case "${STUB_RECOVERY_STOP_MODE:-}" in
                        '') ;;
                        error) exit 1 ;;
                        active) exit 0 ;;
                        *) exit 1 ;;
                    esac
                fi
                [ "${STUB_FAIL_PHASE:-}" != stop ] || exit 1
                [ "${STUB_STOP_LEAVES_ACTIVE:-0}" != 1 ] || exit 0
                printf '%s\n' inactive > "${STUB_STATE_DIR:?}/service-state"
                ;;
            daemon-reload)
                [ "${STUB_FAIL_PHASE:-}" != daemon-reload ] || exit 1
                ;;
            enable)
                [ "${STUB_FAIL_PHASE:-}" != enable ] || exit 1
                ;;
            start)
                [ "${STUB_FAIL_PHASE:-}" != start ] || exit 1
                printf '%s\n' active > "${STUB_STATE_DIR:?}/service-state"
                if [ "${STUB_SAME_INVOCATION:-0}" = 1 ]; then
                    printf '%s\n' old-invocation > "${STUB_STATE_DIR:?}/invocation"
                else
                    printf '%s\n' new-invocation > "${STUB_STATE_DIR:?}/invocation"
                fi
                if [ "${STUB_MUTATE_JSON:-0}" = 1 ]; then
                    printf '%s\n' '{"state":"new-normalized"}' > "${STUB_CLIENTS_FILE:?}"
                fi
                ;;
            show)
                cat "${STUB_STATE_DIR:?}/invocation"
                ;;
            is-enabled)
                [ "${STUB_FAIL_PHASE:-}" != enabled-state ] || exit 1
                ;;
            *) exit 1 ;;
        esac
        ;;
    modprobe)
        if [ "${1:-}" = -r ]; then
            log 'modprobe unload'
            [ "${STUB_FAIL_PHASE:-}" != module-unload ] || exit 1
        else
            log 'modprobe load'
            [ "${STUB_FAIL_PHASE:-}" != module-load ] || exit 1
        fi
        ;;
    modinfo)
        log 'modinfo module'
        [ "${STUB_FAIL_PHASE:-}" != module-info ] || exit 1
        ;;
    ip)
        log "ip $*"
        if [ "${STUB_FOREIGN_INTERFACE:-0}" = 1 ]; then
            printf '%s\n' '7: foreign-awg: <POINTOPOINT> mtu 1420 qdisc noop state DOWN mode DEFAULT group default'
        fi
        ;;
    install)
        log "install $*"
        destination=${!#}
        if [ "$destination" = "${STUB_BINARY_PATH:?}" ] \
            && [ "${STUB_FAIL_PHASE:-}" = binary-install ]; then
            exit 1
        fi
        source=${@: -2:1}
        cp -- "$source" "$destination"
        chmod 0755 "$destination"
        ;;
    cp)
        log "backup-copy $*"
        [ "${STUB_FAIL_PHASE:-}" != backup ] || exit 1
        /bin/cp "$@"
        ;;
    chown)
        log "chown $*"
        ;;
    sysctl)
        log "sysctl $*"
        [ "${STUB_FAIL_PHASE:-}" != sysctl ] || exit 1
        ;;
    timeout)
        log "timeout $*"
        while [[ ${1:-} == --* ]]; do
            shift
        done
        shift
        exec "$@"
        ;;
    sleep)
        exit 1
        ;;
    *)
        exit 1
        ;;
esac
STUB
    chmod 0755 "$stub_dir/stub-command"

    for command in \
        add-apt-repository apt-get awg chown cp curl dpkg dpkg-query install ip \
        modinfo modprobe openssl sha256sum sleep sysctl systemctl timeout; do
        ln -s stub-command "$stub_dir/$command"
    done
}

setup_base_case() {
    local name=$1

    case_dir="$temp_dir/$name"
    mkdir -p "$case_dir" "$case_dir/stubs" "$case_dir/state" "$case_dir/data"
    write_stubs "$case_dir/stubs"

    PATH="$case_dir/stubs:$original_path"
    export PATH

    export STUB_TRACE="$case_dir/trace"
    export STUB_CURL_ARGUMENTS="$case_dir/curl-arguments"
    export STUB_CURL_CONFIG_MODE="$case_dir/curl-config-mode"
    export STUB_CURL_CONFIG_PATH="$case_dir/curl-config-path"
    export STUB_STATE_DIR="$case_dir/state"
    export STUB_BINARY_PATH="$case_dir/bin/awg-server"
    export STUB_CLIENTS_FILE="$case_dir/data/clients.json"
    export STUB_EXPECTED_TOKEN='synthetic-test-bearer-token'
    export STUB_VERSION=1.2.3
    export STUB_TOOLS_VERSION='1.0.20210914-0~202608130145+ee0f0a9~ubuntu22.04.1'
    export STUB_DKMS_VERSION='1.0.0-0~202608271845+b72bb7a~ubuntu22.04.1'
    unset STUB_FAIL_PHASE STUB_FOREIGN_INTERFACE STUB_HEALTH_RESPONSE \
        STUB_CLIENTS_RESPONSE STUB_MUTATE_JSON STUB_SAME_INVOCATION \
        STUB_PACKAGE_STATUS_MODE STUB_PACKAGE_VERSION_MODE \
        STUB_RECOVERY_STOP_MODE STUB_STOP_LEAVES_ACTIVE \
        STUB_SYSTEMCTL_STATUS || true

    printf '%s\n' active > "$case_dir/state/service-state"
    printf '%s\n' old-invocation > "$case_dir/state/invocation"
    : > "$STUB_TRACE"
    : > "$STUB_CURL_ARGUMENTS"
    : > "$case_dir/state/stop-count"

    mkdir -p "$case_dir/bin" "$case_dir/etc" "$case_dir/sysctl" "$case_dir/systemd"
    printf '%s\n' old-binary > "$STUB_BINARY_PATH"
    chmod 0755 "$STUB_BINARY_PATH"

    INSTALLER_BINARY_PATH=$STUB_BINARY_PATH
    INSTALLER_ENV_PATH="$case_dir/etc/awg-server.env"
    INSTALLER_SYSCTL_PATH="$case_dir/sysctl/99-awg-server.conf"
    INSTALLER_UNIT_PATH="$case_dir/systemd/awg-server.service"
    INSTALLER_STAGE_ROOT="$case_dir/stage"
    INSTALLER_BACKUP_ROOT="$case_dir/backups"
    INSTALLER_BACKUP_DIR=''
    INSTALLER_STAGED_BINARY=''
    INSTALLER_SERVICE_GATE_SECONDS=5

    reset_settings
    export AWG_API_TOKEN=$STUB_EXPECTED_TOKEN
    export AWG_ADDRESS=10.0.0.1/24
    export AWG_ENDPOINT=vpn.example.test
    export AWG_DATA_DIR="$case_dir/data"
}

resolve_case_settings() {
    resolve_installer_settings
    AWG_SERVER_VERSION=1.2.3
}

run_transaction() {
    local output_file=$1
    local error_file=$2

    if (install_release_transaction awg-server-linux-amd64) >"$output_file" 2>"$error_file"; then
        return 0
    fi

    return 1
}

assert_recovery_message() {
    local error_file=$1

    assert_file_contains "$error_file" 'RECOVERY: awg-server remains stopped.'
    assert_file_not_contains "$error_file" "$STUB_EXPECTED_TOKEN"
}

assert_unconfirmed_recovery_message() {
    local error_file=$1

    assert_file_contains "$error_file" \
        'RECOVERY: awg-server stop could not be confirmed.'
    assert_file_not_contains "$error_file" 'RECOVERY: awg-server remains stopped.'
    assert_file_not_contains "$error_file" "$STUB_EXPECTED_TOKEN"
}

assert_retained_backup_reported() {
    local error_file=$1
    local backup_root=$2
    local completed_backup

    completed_backup=$(backup_dir "$backup_root")
    assert_file_contains "$error_file" "Backup retained at: $completed_backup"
}

trace_count() {
    local trace_file=$1
    local entry=$2

    grep -c -Fx -- "$entry" "$trace_file" || true
}

test_config_parser_rejects_executable_and_unknown_input() {
    local config_file
    local original_stage_root

    setup_base_case config-parser-unsafe
    config_file="$case_dir/unsafe.env"
    original_stage_root=$INSTALLER_STAGE_ROOT
    export STUB_PARSER_EXECUTED="$case_dir/parser-executed"
    tee "$config_file" >/dev/null <<'EOF'
AWG_API_TOKEN="synthetic-file-token"
AWG_ADDRESS="10.0.0.1/24"
AWG_ENDPOINT="vpn.example.test"
AWG_MTU="$(printf owned > "${STUB_PARSER_EXECUTED:?}")"
EOF
    chmod 0600 "$config_file"

    if load_config_file "$config_file"; then
        fail 'config parser accepted a command substitution'
    fi
    [ ! -e "$STUB_PARSER_EXECUTED" ] \
        || fail 'config parser executed a command substitution'
    [ "$INSTALLER_STAGE_ROOT" = "$original_stage_root" ] \
        || fail 'config parser changed an internal installer path'

    tee "$config_file" >/dev/null <<'EOF'
AWG_API_TOKEN="synthetic-file-token"
AWG_ADDRESS="10.0.0.1/24"
AWG_ENDPOINT="vpn.example.test"
INSTALLER_STAGE_ROOT="/unsafe"
EOF
    if load_config_file "$config_file"; then
        fail 'config parser accepted an unknown assignment'
    fi
    [ "$INSTALLER_STAGE_ROOT" = "$original_stage_root" ] \
        || fail 'unknown config assignment changed an internal installer path'

    tee "$config_file" >/dev/null <<'EOF'
AWG_API_TOKEN="synthetic-file-token"
AWG_ADDRESS="10.0.0.1/24"
AWG_ENDPOINT=unquoted
EOF
    if load_config_file "$config_file"; then
        fail 'config parser accepted malformed syntax'
    fi
}

test_config_parser_round_trips_rendered_values() {
    local config_file
    local special_token='synthetic token with spaces "quotes" \ slash $ dollar ` tick'
    local special_endpoint='vpn example "quoted" \ path $ value ` value'

    setup_base_case config-parser-round-trip
    config_file="$case_dir/round-trip.env"
    AWG_API_TOKEN=$special_token
    AWG_ADDRESS=10.0.0.1/24
    AWG_ENDPOINT=$special_endpoint
    AWG_DNS='dns value with spaces "quotes" \ slash $ dollar ` tick'
    render_environment > "$config_file" || fail 'could not render round-trip environment'

    reset_settings
    load_config_file "$config_file" || fail 'parser rejected rendered environment'
    apply_loaded_config_environment
    [ "$AWG_API_TOKEN" = "$special_token" ] \
        || fail 'parser did not preserve token round-trip bytes'
    [ "$AWG_ENDPOINT" = "$special_endpoint" ] \
        || fail 'parser did not preserve endpoint round-trip bytes'
    [ "$AWG_DNS" = 'dns value with spaces "quotes" \ slash $ dollar ` tick' ] \
        || fail 'parser did not preserve DNS round-trip bytes'
}

test_package_minimum_versions() {
    local version_mode

    for version_mode in exact newer older; do
        setup_base_case "packages-$version_mode"
        export STUB_PACKAGE_VERSION_MODE=$version_mode

        if [ "$version_mode" = older ]; then
            if install_amneziawg >"$case_dir/output" 2>"$case_dir/error"; then
                fail 'older packages unexpectedly passed the minimum gate'
            fi
        else
            install_amneziawg >"$case_dir/output" 2>"$case_dir/error" \
                || fail "$version_mode package versions failed the minimum gate"
        fi

        case "$version_mode" in
            exact)
                assert_file_contains "$STUB_TRACE" \
                    "dpkg --compare-versions $STUB_TOOLS_VERSION ge $STUB_TOOLS_VERSION"
                ;;
            newer)
                assert_file_contains "$STUB_TRACE" \
                    'dpkg --compare-versions 9.9.9-0~synthetic ge'
                ;;
        esac
    done
}

test_package_status_rejection() {
    local status_mode

    for status_mode in missing malformed not-installed; do
        setup_base_case "package-status-$status_mode"
        export STUB_PACKAGE_STATUS_MODE=$status_mode

        if install_amneziawg >"$case_dir/output" 2>"$case_dir/error"; then
            fail "$status_mode package status unexpectedly passed"
        fi
        assert_file_contains "$STUB_TRACE" 'apt-get install -y amneziawg amneziawg-tools amneziawg-dkms'
    done
}

test_settings_precedence_and_rerun() {
    local setting
    local key
    local expected
    local -a rerun_settings=(
        'AWG_DEFAULT_PROTOCOL_VERSION=2.0'
        'AWG31_MTU=1300'
        'AWG31_PERSISTENT_KEEPALIVE=30-40'
        'AWG31_CONTENT_PADDING_ADDITION=20-30'
        'AWG31_REKEY_AFTER_TIME=40-50'
        'AWG31_REKEY_TIMEOUT=6-8'
        'AWG31_REJECT_AFTER_TIME=160-170'
        'AWG31_KEEPALIVE_TIMEOUT=8-12'
        'AWG31_MAX_HANDSHAKE_ATTEMPTS=18-19'
        'AWG31_RANDOM_TRAILERS=off'
        'AWG31_DISABLE_COOKIES=on'
    )

    setup_base_case fresh-settings
    resolve_case_settings

    for setting in \
        'AWG_DEFAULT_PROTOCOL_VERSION=3.1' \
        'AWG31_MTU=1280' \
        'AWG31_PERSISTENT_KEEPALIVE=25-35' \
        'AWG31_CONTENT_PADDING_ADDITION=10-100' \
        'AWG31_REKEY_AFTER_TIME=100-120' \
        'AWG31_REKEY_TIMEOUT=3-7' \
        'AWG31_REJECT_AFTER_TIME=150-180' \
        'AWG31_KEEPALIVE_TIMEOUT=5-15' \
        'AWG31_MAX_HANDSHAKE_ATTEMPTS=15-20' \
        'AWG31_RANDOM_TRAILERS=on' \
        'AWG31_DISABLE_COOKIES=off'; do
        key=${setting%%=*}
        expected=${setting#*=}
        [ "${!key}" = "$expected" ] \
            || fail "fresh setting $key did not use its default"
    done

    setup_base_case rerun-settings
    tee "$INSTALLER_ENV_PATH" >/dev/null <<EOF
AWG_API_TOKEN="synthetic-file-token"
AWG_ADDRESS="10.0.0.1/24"
AWG_ENDPOINT="vpn.example.test"
AWG_DATA_DIR="$case_dir/data"
AWG_DEFAULT_PROTOCOL_VERSION="2.0"
AWG31_MTU="1300"
AWG31_PERSISTENT_KEEPALIVE="30-40"
AWG31_CONTENT_PADDING_ADDITION="20-30"
AWG31_REKEY_AFTER_TIME="40-50"
AWG31_REKEY_TIMEOUT="6-8"
AWG31_REJECT_AFTER_TIME="160-170"
AWG31_KEEPALIVE_TIMEOUT="8-12"
AWG31_MAX_HANDSHAKE_ATTEMPTS="18-19"
AWG31_RANDOM_TRAILERS="off"
AWG31_DISABLE_COOKIES="on"
EOF
    chmod 0600 "$INSTALLER_ENV_PATH"
    export AWG31_MTU=1400
    resolve_case_settings
    write_host_configuration || fail 'could not write rerun configuration'

    for setting in "${rerun_settings[@]}"; do
        key=${setting%%=*}
        expected=${setting#*=}
        if [ "$key" = AWG31_MTU ]; then
            expected=1400
        fi
        [ "${!key}" = "$expected" ] \
            || fail "rerun setting $key did not preserve the selected value"
        assert_file_contains "$INSTALLER_ENV_PATH" "$key=\"$expected\""
    done
}

test_fresh_success_and_backup_permissions() {
    local completed_backup

    setup_base_case fresh-success
    resolve_case_settings
    if ! run_transaction "$case_dir/output" "$case_dir/error"; then
        fail 'fresh transaction failed'
    fi

    completed_backup=$(backup_dir "$INSTALLER_BACKUP_ROOT")
    assert_mode "$INSTALLER_STAGE_ROOT" 700
    assert_mode "$INSTALLER_BACKUP_ROOT" 700
    assert_mode "$completed_backup" 700
    [ ! -e "$completed_backup/environment" ] || fail 'fresh backup copied an absent environment file'
    [ ! -e "$completed_backup/clients.json" ] || fail 'fresh backup copied an absent clients file'
    [ ! -e "$completed_backup/usage.json" ] || fail 'fresh backup copied an absent usage file'
    [ "$(<"$case_dir/state/service-state")" = active ] \
        || fail 'successful service was not active'
}

test_stop_backup_module_order_and_permissions() {
    local completed_backup

    setup_base_case rerun-success
    tee "$INSTALLER_ENV_PATH" >/dev/null <<EOF
AWG_API_TOKEN="$STUB_EXPECTED_TOKEN"
AWG_ADDRESS="10.0.0.1/24"
AWG_ENDPOINT="vpn.example.test"
AWG_DATA_DIR="$case_dir/data"
EOF
    chmod 0600 "$INSTALLER_ENV_PATH"
    printf '%s\n' '{"state":"original"}' > "$case_dir/data/clients.json"
    printf '%s\n' '{"usage":"original"}' > "$case_dir/data/usage.json"
    chmod 0600 "$case_dir/data/clients.json" "$case_dir/data/usage.json"
    resolve_case_settings
    if ! run_transaction "$case_dir/output" "$case_dir/error"; then
        fail 'rerun transaction failed'
    fi

    completed_backup=$(backup_dir "$INSTALLER_BACKUP_ROOT")
    assert_before "$STUB_TRACE" 'systemctl stop' 'backup-copy'
    assert_before "$STUB_TRACE" 'backup-copy' 'modprobe unload'
    assert_before "$STUB_TRACE" 'modprobe unload' 'staged-check-runtime'
    assert_before "$STUB_TRACE" 'staged-check-runtime' 'systemctl start'
    assert_before "$STUB_TRACE" 'curl health' 'curl clients'
    assert_file_contains "$STUB_TRACE" 'old-binary-present-at-check'
    assert_mode "$completed_backup" 700
    assert_mode "$completed_backup/environment" 600
    assert_mode "$completed_backup/clients.json" 600
    assert_mode "$completed_backup/usage.json" 600
    assert_file_contains "$completed_backup/clients.json" '{"state":"original"}'
    assert_file_contains "$completed_backup/usage.json" '{"usage":"original"}'
}

test_foreign_interface_refusal() {
    setup_base_case foreign-interface
    export STUB_FOREIGN_INTERFACE=1
    printf '%s\n' '{"state":"original"}' > "$case_dir/data/clients.json"
    resolve_case_settings
    if run_transaction "$case_dir/output" "$case_dir/error"; then
        fail 'foreign interface did not abort the upgrade'
    fi

    assert_file_contains "$STUB_BINARY_PATH" old-binary
    assert_stopped "$case_dir/state/service-state"
    assert_file_not_contains "$STUB_TRACE" 'modprobe unload'
    assert_file_not_contains "$STUB_TRACE" 'ip link del'
    assert_recovery_message "$case_dir/error"
    assert_retained_backup_reported "$case_dir/error" "$INSTALLER_BACKUP_ROOT"
}

test_before_backup_failures() {
    local phase

    for phase in apt signature checksum stop; do
        setup_base_case "before-backup-$phase"
        export STUB_FAIL_PHASE=$phase
        resolve_case_settings
        if run_transaction "$case_dir/output" "$case_dir/error"; then
            fail "$phase unexpectedly passed"
        fi

        assert_file_contains "$STUB_BINARY_PATH" old-binary
        [ "$(<"$case_dir/state/service-state")" = active ] \
            || fail "$phase stopped an existing service before a completed stage"
        [ ! -d "$INSTALLER_BACKUP_ROOT" ] \
            || fail "$phase created a backup before the graceful stop boundary"
        assert_file_not_contains "$STUB_TRACE" 'modprobe unload'
    done

    setup_base_case before-backup-copy
    export STUB_FAIL_PHASE=backup
    printf '%s\n' '{"state":"original"}' > "$case_dir/data/clients.json"
    resolve_case_settings
    if run_transaction "$case_dir/output" "$case_dir/error"; then
        fail 'backup copy failure unexpectedly passed'
    fi

    assert_file_contains "$STUB_BINARY_PATH" old-binary
    assert_stopped "$case_dir/state/service-state"
    assert_file_not_contains "$STUB_TRACE" 'modprobe unload'
    assert_recovery_message "$case_dir/error"
}

test_unreadable_service_state_refusal() {
    setup_base_case unreadable-service-state
    export STUB_SYSTEMCTL_STATUS=5
    resolve_case_settings
    if run_transaction "$case_dir/output" "$case_dir/error"; then
        fail 'unknown systemctl state unexpectedly allowed a module transaction'
    fi

    assert_file_contains "$STUB_BINARY_PATH" old-binary
    [ ! -d "$INSTALLER_BACKUP_ROOT" ] \
        || fail 'unknown systemctl state crossed the backup boundary'
    assert_file_not_contains "$STUB_TRACE" 'modprobe unload'
}

test_stop_failures_do_not_make_false_recovery_claims() {
    local recovery_mode
    local starts

    for recovery_mode in command partial; do
        setup_base_case "early-stop-$recovery_mode"
        if [ "$recovery_mode" = command ]; then
            export STUB_FAIL_PHASE=stop
        else
            export STUB_STOP_LEAVES_ACTIVE=1
        fi
        resolve_case_settings
        if run_transaction "$case_dir/output" "$case_dir/error"; then
            fail "$recovery_mode early stop failure unexpectedly passed"
        fi

        assert_file_contains "$STUB_BINARY_PATH" old-binary
        [ "$(<"$case_dir/state/service-state")" = active ] \
            || fail "$recovery_mode early stop failure did not leave the service active"
        [ ! -d "$INSTALLER_BACKUP_ROOT" ] \
            || fail "$recovery_mode early stop failure crossed the backup boundary"
        assert_file_not_contains "$STUB_TRACE" 'modprobe unload'
        assert_file_not_contains "$case_dir/error" 'RECOVERY:'
        starts=$(trace_count "$STUB_TRACE" 'systemctl start')
        [ "$starts" = 0 ] \
            || fail "$recovery_mode early stop failure started a transaction service"
    done

    for recovery_mode in error active; do
        setup_base_case "recovery-stop-$recovery_mode"
        export STUB_FAIL_PHASE=health
        export STUB_RECOVERY_STOP_MODE=$recovery_mode
        resolve_case_settings
        if run_transaction "$case_dir/output" "$case_dir/error"; then
            fail "$recovery_mode failed recovery stop unexpectedly passed"
        fi

        assert_file_contains "$STUB_BINARY_PATH" 'staged-check-runtime'
        [ "$(<"$case_dir/state/service-state")" = active ] \
            || fail "$recovery_mode failed recovery stop was not observable as active"
        assert_unconfirmed_recovery_message "$case_dir/error"
        assert_retained_backup_reported "$case_dir/error" "$INSTALLER_BACKUP_ROOT"
        starts=$(trace_count "$STUB_TRACE" 'systemctl start')
        [ "$starts" = 1 ] \
            || fail "$recovery_mode failure triggered an automatic restart"
    done
}

test_pre_replacement_failures() {
    local phase

    for phase in module-unload module-load module-info check-runtime; do
        setup_base_case "pre-$phase"
        export STUB_FAIL_PHASE=$phase
        printf '%s\n' '{"state":"original"}' > "$case_dir/data/clients.json"
        resolve_case_settings
        if run_transaction "$case_dir/output" "$case_dir/error"; then
            fail "$phase unexpectedly passed"
        fi

        assert_file_contains "$STUB_BINARY_PATH" old-binary
        assert_stopped "$case_dir/state/service-state"
        backup_dir "$INSTALLER_BACKUP_ROOT" >/dev/null
        assert_recovery_message "$case_dir/error"
        assert_retained_backup_reported "$case_dir/error" "$INSTALLER_BACKUP_ROOT"
    done
}

test_post_replacement_failure_preserves_new_state() {
    local completed_backup
    local starts

    setup_base_case post-replacement
    export STUB_FAIL_PHASE=health
    export STUB_MUTATE_JSON=1
    tee "$INSTALLER_ENV_PATH" >/dev/null <<EOF
AWG_API_TOKEN="$STUB_EXPECTED_TOKEN"
AWG_ADDRESS="10.0.0.1/24"
AWG_ENDPOINT="vpn.example.test"
AWG_DATA_DIR="$case_dir/data"
AWG_MTU="1350"
EOF
    chmod 0600 "$INSTALLER_ENV_PATH"
    printf '%s\n' '{"state":"original"}' > "$case_dir/data/clients.json"
    resolve_case_settings
    if run_transaction "$case_dir/output" "$case_dir/error"; then
        fail 'failed health gate unexpectedly passed'
    fi

    completed_backup=$(backup_dir "$INSTALLER_BACKUP_ROOT")
    assert_file_contains "$STUB_BINARY_PATH" 'staged-check-runtime'
    assert_file_contains "$INSTALLER_ENV_PATH" 'AWG_DEFAULT_PROTOCOL_VERSION="3.1"'
    assert_file_contains "$case_dir/data/clients.json" '{"state":"new-normalized"}'
    assert_file_contains "$completed_backup/clients.json" '{"state":"original"}'
    assert_stopped "$case_dir/state/service-state"
    starts=$(grep -c -Fx 'systemctl start' "$STUB_TRACE" || true)
    [ "$starts" = 1 ] || fail 'installer automatically restarted a failed service'
    assert_recovery_message "$case_dir/error"
    assert_retained_backup_reported "$case_dir/error" "$INSTALLER_BACKUP_ROOT"
}

test_post_replacement_failure_phases() {
    local phase

    for phase in binary-install sysctl daemon-reload enable start enabled-state; do
        setup_base_case "post-$phase"
        export STUB_FAIL_PHASE=$phase
        resolve_case_settings
        if run_transaction "$case_dir/output" "$case_dir/error"; then
            fail "$phase unexpectedly passed"
        fi

        assert_stopped "$case_dir/state/service-state"
        backup_dir "$INSTALLER_BACKUP_ROOT" >/dev/null
        if [ "$phase" != binary-install ]; then
            assert_file_contains "$STUB_BINARY_PATH" 'staged-check-runtime'
        fi
        assert_recovery_message "$case_dir/error"
        assert_retained_backup_reported "$case_dir/error" "$INSTALLER_BACKUP_ROOT"
    done
}

prepare_post_replacement_case() {
    local name=$1

    setup_base_case "$name"
    tee "$INSTALLER_ENV_PATH" >/dev/null <<EOF
AWG_API_TOKEN="$STUB_EXPECTED_TOKEN"
AWG_ADDRESS="10.0.0.1/24"
AWG_ENDPOINT="vpn.example.test"
AWG_DATA_DIR="$case_dir/data"
AWG_MTU="1350"
EOF
    chmod 0600 "$INSTALLER_ENV_PATH"
    printf '%s\n' '{"state":"original"}' > "$case_dir/data/clients.json"
    printf '%s\n' '{"usage":"original"}' > "$case_dir/data/usage.json"
    chmod 0600 "$case_dir/data/clients.json" "$case_dir/data/usage.json"
    export STUB_MUTATE_JSON=1
    resolve_case_settings
}

test_post_replacement_failure_postconditions() {
    local phase
    local completed_backup
    local expected_binary
    local expected_config
    local expected_json
    local expected_starts
    local config_path
    local starts

    for phase in \
        binary-install sysctl daemon-reload enable start enabled-state invocation \
        health auth-transport clients clients-json; do
        prepare_post_replacement_case "postconditions-$phase"
        expected_binary=1
        expected_config=1
        expected_json=0
        expected_starts=1

        case "$phase" in
            binary-install)
                export STUB_FAIL_PHASE=binary-install
                expected_binary=0
                expected_config=0
                expected_starts=0
                ;;
            sysctl | daemon-reload | enable)
                export STUB_FAIL_PHASE=$phase
                expected_starts=0
                ;;
            start)
                export STUB_FAIL_PHASE=start
                ;;
            enabled-state)
                export STUB_FAIL_PHASE=enabled-state
                expected_json=1
                ;;
            invocation)
                export STUB_SAME_INVOCATION=1
                expected_json=1
                ;;
            health | auth-transport | clients | clients-json)
                export STUB_FAIL_PHASE=$phase
                expected_json=1
                ;;
            *) fail "unknown post-replacement phase $phase" ;;
        esac

        if run_transaction "$case_dir/output" "$case_dir/error"; then
            fail "$phase post-replacement failure unexpectedly passed"
        fi

        completed_backup=$(backup_dir "$INSTALLER_BACKUP_ROOT")
        assert_file_contains "$completed_backup/environment" 'AWG_MTU="1350"'
        assert_file_contains "$completed_backup/clients.json" '{"state":"original"}'
        assert_file_contains "$completed_backup/usage.json" '{"usage":"original"}'
        assert_file_not_contains "$STUB_TRACE" \
            "backup-copy -- $completed_backup/clients.json $case_dir/data/clients.json"
        assert_stopped "$case_dir/state/service-state"
        assert_recovery_message "$case_dir/error"
        assert_retained_backup_reported "$case_dir/error" "$INSTALLER_BACKUP_ROOT"

        if [ "$expected_binary" = 1 ]; then
            assert_file_contains "$STUB_BINARY_PATH" 'staged-check-runtime'
        else
            assert_file_contains "$STUB_BINARY_PATH" old-binary
        fi
        if [ "$expected_config" = 1 ]; then
            assert_file_contains "$INSTALLER_ENV_PATH" \
                'AWG_DEFAULT_PROTOCOL_VERSION="3.1"'
        else
            assert_file_not_contains "$INSTALLER_ENV_PATH" \
                'AWG_DEFAULT_PROTOCOL_VERSION="3.1"'
        fi
        if [ "$expected_json" = 1 ]; then
            assert_file_contains "$case_dir/data/clients.json" \
                '{"state":"new-normalized"}'
        else
            assert_file_contains "$case_dir/data/clients.json" '{"state":"original"}'
        fi

        starts=$(trace_count "$STUB_TRACE" 'systemctl start')
        [ "$starts" = "$expected_starts" ] \
            || fail "$phase automatic restart count was $starts, want $expected_starts"

        case "$phase" in
            auth-transport | clients | clients-json)
                [ "$(<"$STUB_CURL_CONFIG_MODE")" = 600 ] \
                    || fail "$phase curl authorization config was not root-only"
                config_path=$(<"$STUB_CURL_CONFIG_PATH")
                [ ! -e "$config_path" ] \
                    || fail "$phase retained a curl authorization config"
                assert_file_not_contains "$STUB_CURL_ARGUMENTS" \
                    "$STUB_EXPECTED_TOKEN"
                ;;
        esac
    done
}

test_authenticated_gate_and_json_failures() {
    local phase
    local config_path

    setup_base_case authenticated-success
    resolve_case_settings
    if ! run_transaction "$case_dir/output" "$case_dir/error"; then
        fail 'authenticated client-list gate failed'
    fi

    [ "$(<"$STUB_CURL_CONFIG_MODE")" = 600 ] \
        || fail 'curl authorization config was not root-only'
    config_path=$(<"$STUB_CURL_CONFIG_PATH")
    [ ! -e "$config_path" ] || fail 'curl authorization config was retained'
    assert_file_not_contains "$STUB_CURL_ARGUMENTS" "$STUB_EXPECTED_TOKEN"
    assert_file_not_contains "$case_dir/output" "$STUB_EXPECTED_TOKEN"
    assert_file_not_contains "$case_dir/error" "$STUB_EXPECTED_TOKEN"

    for phase in auth-transport clients clients-json; do
        setup_base_case "authenticated-$phase"
        export STUB_FAIL_PHASE=$phase
        resolve_case_settings
        if run_transaction "$case_dir/output" "$case_dir/error"; then
            fail "$phase client-list gate unexpectedly passed"
        fi

        assert_stopped "$case_dir/state/service-state"
        assert_recovery_message "$case_dir/error"
        assert_retained_backup_reported "$case_dir/error" "$INSTALLER_BACKUP_ROOT"
    done
}

test_exact_health_response_is_required() {
    setup_base_case non-exact-health
    export STUB_HEALTH_RESPONSE='{"status":"ok"} '
    resolve_case_settings
    if run_transaction "$case_dir/output" "$case_dir/error"; then
        fail 'non-exact health response unexpectedly passed'
    fi

    assert_stopped "$case_dir/state/service-state"
    assert_recovery_message "$case_dir/error"
    assert_retained_backup_reported "$case_dir/error" "$INSTALLER_BACKUP_ROOT"
}

test_token_newlines_are_rejected_before_curl_config() {
    setup_base_case token-newline
    resolve_case_settings
    AWG_API_TOKEN=$'synthetic-test-bearer-token\ninjected = true'

    prepare_stage_directory || fail 'could not prepare stage for token validation'
    if create_curl_auth_config >"$case_dir/curl-config"; then
        fail 'token newline was written into the curl authorization config'
    fi

    [ ! -s "$case_dir/curl-config" ] \
        || fail 'token validation returned a curl authorization config path'
    [ -z "$(find "$INSTALLER_STAGE_ROOT" -maxdepth 1 -name 'curl-auth.*' -print)" ] \
        || fail 'token validation created a curl authorization config'
}

test_curl_config_is_cleaned_on_parent_termination() {
    local config_path

    setup_base_case curl-config-cleanup
    resolve_case_settings
    export INSTALLER_TEST_SCRIPT=$installer
    export TEST_CONFIG_PATH="$case_dir/curl-config-path"
    export TEST_STAGE_ROOT=$INSTALLER_STAGE_ROOT
    tee "$case_dir/cleanup-probe.sh" >/dev/null <<'PROBE'
#!/usr/bin/env bash
set -Eeuo pipefail

source "$INSTALLER_TEST_SCRIPT"
INSTALLER_STAGE_ROOT=$TEST_STAGE_ROOT
INSTALLER_STAGE_DIR=''
INSTALL_TEMP_PATHS=()

trap cleanup_temp_paths EXIT
trap handle_interruption HUP INT TERM
prepare_stage_directory
config_path=$(create_curl_auth_config)
printf '%s\n' "$config_path" > "$TEST_CONFIG_PATH"
kill -TERM "$$"
PROBE
    chmod 0755 "$case_dir/cleanup-probe.sh"
    if "$BASH" "$case_dir/cleanup-probe.sh"; then
        fail 'controlled termination unexpectedly returned success'
    fi
    config_path=$(<"$case_dir/curl-config-path")
    [ ! -e "$config_path" ] \
        || fail 'curl authorization config survived parent termination cleanup'
    assert_file_not_contains "$STUB_CURL_ARGUMENTS" "$STUB_EXPECTED_TOKEN"
}

test_invocation_gate() {
    setup_base_case unchanged-invocation
    export STUB_SAME_INVOCATION=1
    resolve_case_settings
    if run_transaction "$case_dir/output" "$case_dir/error"; then
        fail 'unchanged InvocationID unexpectedly passed'
    fi

    assert_stopped "$case_dir/state/service-state"
    assert_recovery_message "$case_dir/error"
    assert_retained_backup_reported "$case_dir/error" "$INSTALLER_BACKUP_ROOT"
}

# shellcheck source=install.sh
source "$installer"

test_config_parser_rejects_executable_and_unknown_input
test_config_parser_round_trips_rendered_values
test_package_minimum_versions
test_package_status_rejection
test_settings_precedence_and_rerun
test_fresh_success_and_backup_permissions
test_stop_backup_module_order_and_permissions
test_foreign_interface_refusal
test_before_backup_failures
test_unreadable_service_state_refusal
test_stop_failures_do_not_make_false_recovery_claims
test_pre_replacement_failures
test_post_replacement_failure_preserves_new_state
test_post_replacement_failure_phases
test_post_replacement_failure_postconditions
test_authenticated_gate_and_json_failures
test_exact_health_response_is_required
test_token_newlines_are_rejected_before_curl_config
test_curl_config_is_cleaned_on_parent_termination
test_invocation_gate

printf 'install tests passed\n'
