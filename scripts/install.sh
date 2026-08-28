#!/usr/bin/env bash
set -Eeuo pipefail

readonly RELEASE_PUBLIC_KEY_BASE64='LS0tLS1CRUdJTiBQVUJMSUMgS0VZLS0tLS0KTUNvd0JRWURLMlZ3QXlFQWQrdGtVUzJlTElwMFlpU1FFdHMwYk9CcmNmcTEvZDBQT0pXd29Udyt1NzA9Ci0tLS0tRU5EIFBVQkxJQyBLRVktLS0tLQo='
readonly LATEST_MANIFEST_URL='https://github.com/StealthSurf-VPN/awg-server/releases/latest/download/SHA256SUMS'
readonly MINIMUM_TOOLS_PACKAGE_VERSION='1.0.20210914-0~202608130145+ee0f0a9~ubuntu22.04.1'
readonly MINIMUM_DKMS_PACKAGE_VERSION='1.0.0-0~202608271845+b72bb7a~ubuntu22.04.1'
readonly RECOVERY_GUIDANCE='RECOVERY: awg-server remains stopped. Retain the root-only backup and recover manually; do not restart an unqualified binary.'
readonly UNCONFIRMED_RECOVERY_GUIDANCE='RECOVERY: awg-server stop could not be confirmed. Do not assume the service is stopped; retain the root-only backup and intervene manually.'
readonly -a EXPECTED_RELEASE_ASSETS=(
    awg-server-awg31-darwin-amd64
    awg-server-awg31-darwin-arm64
    awg-server-awg31-linux-amd64
    awg-server-awg31-linux-arm64
    awg-server-awg31-windows-amd64.exe
    awg-server-awg31-windows-arm64.exe
)
readonly -a ENVIRONMENT_KEYS=(
    AWG_API_TOKEN
    AWG_ADDRESS
    AWG_ENDPOINT
    AWG_LISTEN_PORT
    AWG_HTTP_PORT
    AWG_MTU
    AWG_DNS
    AWG_DATA_DIR
    AWG_INTERFACE
    AWG_JC
    AWG_JMIN
    AWG_JMAX
    AWG_S3
    AWG_S4
    AWG_I1
    AWG_I2
    AWG_I3
    AWG_I4
    AWG_I5
    AWG_MAX_INTERFACES
    AWG_DEFAULT_PROTOCOL_VERSION
    AWG31_MTU
    AWG31_PERSISTENT_KEEPALIVE
    AWG31_CONTENT_PADDING_ADDITION
    AWG31_REKEY_AFTER_TIME
    AWG31_REKEY_TIMEOUT
    AWG31_REJECT_AFTER_TIME
    AWG31_KEEPALIVE_TIMEOUT
    AWG31_MAX_HANDSHAKE_ATTEMPTS
    AWG31_RANDOM_TRAILERS
    AWG31_DISABLE_COOKIES
)
readonly -a REQUIRED_KEYS=(
    AWG_API_TOKEN
    AWG_ENDPOINT
    AWG_ADDRESS
)

AWG_SERVER_VERSION=''
PROCESS_ENV_PRESENT=()
PROCESS_ENV_VALUES=()
LOADED_CONFIG_PRESENT=()
LOADED_CONFIG_VALUES=()
INSTALL_TEMP_PATHS=()
INSTALLER_BINARY_PATH=/usr/local/bin/awg-server
INSTALLER_ENV_PATH=/etc/awg-server.env
INSTALLER_SYSCTL_PATH=/etc/sysctl.d/99-awg-server.conf
INSTALLER_UNIT_PATH=/etc/systemd/system/awg-server.service
INSTALLER_STAGE_ROOT=/var/lib/awg-server/installer-stage
INSTALLER_STAGE_DIR=''
INSTALLER_STAGED_BINARY=''
INSTALLER_BACKUP_ROOT=/var/backups/awg-server
INSTALLER_BACKUP_DIR=''
INSTALLER_SERVICE_GATE_SECONDS=30

die() {
    printf 'install failed: %s\n' "$1" >&2
    exit 1
}

cleanup_temp_paths() {
    local path

    ((${#INSTALL_TEMP_PATHS[@]} > 0)) || return 0
    for path in "${INSTALL_TEMP_PATHS[@]}"; do
        rm -rf -- "$path" || true
    done
}

capture_process_environment() {
    local key

    PROCESS_ENV_PRESENT=()
    PROCESS_ENV_VALUES=()
    for key in "${ENVIRONMENT_KEYS[@]}"; do
        if [[ ${!key+x} ]]; then
            PROCESS_ENV_PRESENT+=(1)
            PROCESS_ENV_VALUES+=("${!key}")
        else
            PROCESS_ENV_PRESENT+=(0)
            PROCESS_ENV_VALUES+=('')
        fi
    done
}

initialize_loaded_config() {
    local index

    LOADED_CONFIG_PRESENT=()
    LOADED_CONFIG_VALUES=()
    for ((index = 0; index < ${#ENVIRONMENT_KEYS[@]}; index++)); do
        LOADED_CONFIG_PRESENT+=(0)
        LOADED_CONFIG_VALUES+=('')
    done
}

environment_key_index() {
    local candidate=$1
    local result_variable=$2
    local candidate_index

    for ((candidate_index = 0; candidate_index < ${#ENVIRONMENT_KEYS[@]}; candidate_index++)); do
        if [[ ${ENVIRONMENT_KEYS[candidate_index]} == "$candidate" ]]; then
            printf -v "$result_variable" '%s' "$candidate_index"
            return 0
        fi
    done

    return 1
}

decode_rendered_environment_value() {
    local encoded=$1
    local result_variable=$2
    local decoded_value=''
    local escaped
    local index=0
    local length=${#encoded}
    local character

    while ((index < length)); do
        character=${encoded:index:1}
        case $character in
            \\)
                ((index += 1))
                ((index < length)) || return 1
                escaped=${encoded:index:1}
                case $escaped in
                    \\ | '"' | '$' | '`') decoded_value+=$escaped ;;
                    *) return 1 ;;
                esac
                ;;
            '"' | '$' | '`') return 1 ;;
            *) decoded_value+=$character ;;
        esac
        ((index += 1))
    done

    printf -v "$result_variable" '%s' "$decoded_value"
}

load_config_file() {
    local config_file=${1:-}
    local assignment_pattern='^([A-Z][A-Z0-9_]*)="(.*)"$'
    local decoded
    local encoded
    local index
    local key
    local line
    local mode

    [[ -f $config_file && -O $config_file ]] || return 1

    if mode=$(stat -c '%a' -- "$config_file" 2>/dev/null); then
        :
    elif mode=$(stat -f '%Lp' "$config_file" 2>/dev/null); then
        :
    else
        return 1
    fi

    [[ $mode =~ ^[0-7]+$ ]] || return 1
    (( (8#$mode & 0022) == 0 )) \
        || return 1

    initialize_loaded_config
    while IFS= read -r line || [[ -n $line ]]; do
        [[ $line != *$'\r'* && $line =~ $assignment_pattern ]] || return 1
        key=${BASH_REMATCH[1]}
        encoded=${BASH_REMATCH[2]}
        environment_key_index "$key" index || return 1
        [[ ${LOADED_CONFIG_PRESENT[index]} == 0 ]] || return 1
        decode_rendered_environment_value "$encoded" decoded || return 1
        LOADED_CONFIG_PRESENT[index]=1
        LOADED_CONFIG_VALUES[index]=$decoded
    done < "$config_file"
}

apply_loaded_config_environment() {
    local index
    local key

    for ((index = 0; index < ${#ENVIRONMENT_KEYS[@]}; index++)); do
        [[ ${LOADED_CONFIG_PRESENT[index]:-0} == 1 ]] || continue
        key=${ENVIRONMENT_KEYS[index]}
        printf -v "$key" '%s' "${LOADED_CONFIG_VALUES[index]}"
    done
}

clear_non_process_environment() {
    local index
    local key

    for ((index = 0; index < ${#ENVIRONMENT_KEYS[@]}; index++)); do
        [[ ${PROCESS_ENV_PRESENT[index]:-0} == 1 ]] && continue
        key=${ENVIRONMENT_KEYS[index]}
        unset "$key"
    done
}

restore_process_environment() {
    local index key

    for ((index = 0; index < ${#ENVIRONMENT_KEYS[@]}; index++)); do
        [[ ${PROCESS_ENV_PRESENT[index]:-0} == 1 ]] || continue
        key=${ENVIRONMENT_KEYS[index]}
        printf -v "$key" '%s' "${PROCESS_ENV_VALUES[index]}"
        export "${key?}"
    done
}

prompt_setting() {
    local key=${1:-}
    local prompt=${2:-$key}
    local value

    if [[ $key == AWG_API_TOKEN ]]; then
        read -r -s -p "$prompt: " value
        printf '\n' >&2
    else
        read -r -p "$prompt: " value
    fi
    [[ -n $value || $key != AWG_ADDRESS ]] || value=10.0.0.1/24
    [[ -n $value ]] || die "$key is required"
    printf -v "$key" '%s' "$value"
}

require_setting() {
    local key=${1:-}
    local prompt=${2:-$key}

    [[ -n ${!key:-} ]] && return
    [[ -t 0 ]] || die "$key is required in non-interactive mode"

    prompt_setting "$key" "$prompt"
}

render_environment() {
    local key value

    for key in "${ENVIRONMENT_KEYS[@]}"; do
        [[ ${!key+x} ]] || continue
        value=${!key}
        if [[ $value == *$'\r'* || $value == *$'\n'* ]]; then
            printf '%s\n' "$key must not contain carriage returns or newlines" >&2
            return 1
        fi
    done

    for key in "${ENVIRONMENT_KEYS[@]}"; do
        [[ ${!key+x} ]] || continue
        value=${!key}
        value=${value//\\/\\\\}
        value=${value//\"/\\\"}
        value=${value//\$/\\\$}
        value=${value//\`/\\\`}
        printf '%s="%s"\n' "$key" "$value"
    done
}

set_default_setting() {
    local key=$1
    local value=$2

    [[ ${!key+x} ]] || printf -v "$key" '%s' "$value"
}

apply_awg31_defaults() {
    set_default_setting AWG_DEFAULT_PROTOCOL_VERSION 3.1
    set_default_setting AWG31_MTU 1280
    set_default_setting AWG31_PERSISTENT_KEEPALIVE 25-35
    set_default_setting AWG31_CONTENT_PADDING_ADDITION 10-100
    set_default_setting AWG31_REKEY_AFTER_TIME 100-120
    set_default_setting AWG31_REKEY_TIMEOUT 3-7
    set_default_setting AWG31_REJECT_AFTER_TIME 150-180
    set_default_setting AWG31_KEEPALIVE_TIMEOUT 5-15
    set_default_setting AWG31_MAX_HANDSHAKE_ATTEMPTS 15-20
    set_default_setting AWG31_RANDOM_TRAILERS on
    set_default_setting AWG31_DISABLE_COOKIES off
}

resolve_installer_settings() {
    local key

    capture_process_environment
    clear_non_process_environment
    if [[ -e $INSTALLER_ENV_PATH ]]; then
        load_config_file "$INSTALLER_ENV_PATH" \
            || die 'could not safely parse the existing awg-server environment'
        apply_loaded_config_environment
    fi
    restore_process_environment
    apply_awg31_defaults

    for key in "${REQUIRED_KEYS[@]}"; do
        require_setting "$key"
    done
    select_data_dir /data /var/lib/awg-server
}

select_data_dir() {
    local legacy_dir=$1
    local default_dir=$2

    [[ -n ${AWG_DATA_DIR:-} ]] && return
    if [[ -f $legacy_dir/clients.json ]]; then
        AWG_DATA_DIR=$legacy_dir
    else
        AWG_DATA_DIR=$default_dir
    fi
}

release_asset_name() {
    case ${1:-} in
        x86_64) printf 'awg-server-awg31-linux-amd64\n' ;;
        aarch64 | arm64) printf 'awg-server-awg31-linux-arm64\n' ;;
        *)
            printf 'unsupported architecture: %s\n' "${1:-unknown}" >&2
            return 1
            ;;
    esac
}

resolve_latest_version() {
    local headers
    local location
    local pattern='^https://github\.com/StealthSurf-VPN/awg-server/releases/download/v((0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*))/SHA256SUMS$'

    headers=$(curl -fsSI --proto '=https' --tlsv1.2 "$LATEST_MANIFEST_URL") \
        || die 'could not resolve the latest stable release'
    location=$(printf '%s\n' "$headers" | awk \
        'tolower($1) == "location:" { sub(/\r$/, "", $2); print $2; exit }')
    [[ $location =~ $pattern ]] \
        || die 'latest release redirect is not canonical'
    printf '%s\n' "${BASH_REMATCH[1]}"
}

health_response_ok() {
    [[ ${1:-} == '{"status":"ok"}' ]]
}

clients_response_is_array() {
    printf '%s' "${1:-}" \
        | python3 -c 'import json, sys; value = json.load(sys.stdin); raise SystemExit(not isinstance(value, list))' \
            >/dev/null 2>&1
}

create_curl_auth_config() {
    local config_file
    local token

    [[ $AWG_API_TOKEN != *$'\r'* && $AWG_API_TOKEN != *$'\n'* ]] || return 1
    [[ -n $INSTALLER_STAGE_DIR && -d $INSTALLER_STAGE_DIR ]] || return 1

    config_file=$(mktemp "$INSTALLER_STAGE_DIR/curl-auth.XXXXXX") || return 1
    chown root:root "$config_file" || {
        rm -f -- "$config_file"
        return 1
    }
    chmod 0600 "$config_file" || {
        rm -f -- "$config_file"
        return 1
    }

    token=${AWG_API_TOKEN//\\/\\\\}
    token=${token//\"/\\\"}
    if ! printf 'header = "Authorization: Bearer %s"\n' "$token" > "$config_file"; then
        rm -f -- "$config_file"
        return 1
    fi

    printf '%s\n' "$config_file"
}

authenticated_clients_before_deadline() {
    local config_file
    local deadline=$1
    local health_port=$2
    local response

    if ! config_file=$(create_curl_auth_config); then
        return 1
    fi

    if ! response=$(run_before_deadline "$deadline" curl --disable \
        --config "$config_file" --noproxy '*' --max-time 1 \
        -fsS "http://127.0.0.1:$health_port/api/clients" 2>/dev/null); then
        rm -f -- "$config_file"
        return 1
    fi
    rm -f -- "$config_file"

    clients_response_is_array "$response"
}

invocation_changed() {
    local previous=${1:-}
    local current=${2:-}

    [[ -n $current && $current != "$previous" ]]
}

current_time_milliseconds() {
    local fraction
    local now=${EPOCHREALTIME:-}
    local seconds

    if [[ -z $now ]]; then
        seconds=$(date +%s)
        printf '%d\n' "$((seconds * 1000))"
        return
    fi

    seconds=${now%.*}
    fraction=${now#*.}
    printf '%d\n' "$((10#$seconds * 1000 + 10#${fraction:0:3}))"
}

deadline_after_seconds() {
    local now
    local seconds=$1

    now=$(current_time_milliseconds)
    printf '%d\n' "$((now + seconds * 1000))"
}

remaining_milliseconds() {
    local deadline=$1
    local now
    local remaining

    now=$(current_time_milliseconds)
    remaining=$((deadline - now))
    ((remaining > 0)) || return 1
    printf '%d\n' "$remaining"
}

run_before_deadline() {
    local deadline=$1
    local duration
    local remaining

    shift
    remaining=$(remaining_milliseconds "$deadline") || return 124
    printf -v duration '%d.%03ds' \
        "$((remaining / 1000))" "$((remaining % 1000))"
    timeout --foreground --signal=KILL "$duration" "$@"
}

service_healthy_before_deadline() {
    local deadline=$1
    local previous_invocation=$2
    local health_port=$3
    local current_invocation
    local response

    if ! current_invocation=$(run_before_deadline "$deadline" systemctl show \
        --property=InvocationID --value awg-server.service 2>/dev/null); then
        return 1
    fi
    invocation_changed "$previous_invocation" "$current_invocation" \
        || return 1
    run_before_deadline "$deadline" \
        systemctl is-active --quiet awg-server.service || return 1
    if ! response=$(run_before_deadline "$deadline" curl -fsS --noproxy '*' \
        --max-time 1 "http://127.0.0.1:$health_port/health" 2>/dev/null); then
        return 1
    fi
    health_response_ok "$response" || return 1
    authenticated_clients_before_deadline "$deadline" "$health_port" || return 1
}

require_supported_host() {
    local ID=''
    local VERSION_ID=''

    ((EUID == 0)) || die 'must run as root'
    [[ -r /etc/os-release ]] || die '/etc/os-release is required'

    # shellcheck disable=SC1091
    source /etc/os-release
    [[ $ID == ubuntu && $VERSION_ID == 22.04 ]] \
        || die 'Ubuntu 22.04 is required'

    if [[ -e /.dockerenv || -e /run/.containerenv ]] \
        || { command -v systemd-detect-virt >/dev/null 2>&1 \
            && systemd-detect-virt --quiet --container; }; then
        die 'containers are not supported; use a host or VM'
    fi
}

install_amneziawg() {
    apt-get update || return 1
    DEBIAN_FRONTEND=noninteractive apt-get install -y \
        build-essential ca-certificates curl dkms gnupg2 iproute2 iptables \
        libelf-dev openssl python3-launchpadlib software-properties-common \
        "linux-headers-$(uname -r)" || return 1

    if [[ $(uname -r) == *xanmod* ]]; then
        install -d -m 0755 /etc/apt/keyrings || return 1
        curl -fsSL https://apt.llvm.org/llvm-snapshot.gpg.key \
            | gpg --batch --yes --dearmor \
                -o /etc/apt/keyrings/llvm-snapshot.gpg || return 1
        printf '%s\n' \
            'deb [signed-by=/etc/apt/keyrings/llvm-snapshot.gpg] https://apt.llvm.org/jammy/ llvm-toolchain-jammy-19 main' \
            > /etc/apt/sources.list.d/llvm-19.list || return 1
        apt-get update || return 1
        DEBIAN_FRONTEND=noninteractive apt-get install -y \
            clang-19 lld-19 llvm-19 || return 1
        for tool in \
            clang clang++ ld.lld llvm-ar llvm-nm llvm-objcopy llvm-objdump \
            llvm-readelf llvm-strip; do
            update-alternatives --install \
                "/usr/bin/$tool" "$tool" "/usr/bin/$tool-19" 190 || return 1
            update-alternatives --set "$tool" "/usr/bin/$tool-19" || return 1
        done
        export PATH="/usr/lib/llvm-19/bin:$PATH"
    fi

    add-apt-repository -y ppa:amnezia/ppa || return 1
    apt-get update || return 1
    DEBIAN_FRONTEND=noninteractive apt-get install -y \
        amneziawg amneziawg-tools amneziawg-dkms || return 1
    require_package_minimum amneziawg-tools "$MINIMUM_TOOLS_PACKAGE_VERSION" \
        || return 1
    require_package_minimum amneziawg-dkms "$MINIMUM_DKMS_PACKAGE_VERSION" \
        || return 1
}

installed_package_version() {
    local package_name=$1
    local output
    local status
    local version
    local extra

    output=$(dpkg-query -W -f='${db:Status-Abbrev}\t${Version}\n' "$package_name") \
        || return 1
    IFS=$'\t' read -r status version extra <<< "$output"
    [[ -z ${extra:-} && ${#status} -eq 3 && ${status:1:1} == i \
        && ${status:2:1} == ' ' && -n $version ]] || return 1

    printf '%s\n' "$version"
}

require_package_minimum() {
    local package_name=$1
    local minimum_version=$2
    local installed_version

    installed_version=$(installed_package_version "$package_name") || return 1
    dpkg --compare-versions "$installed_version" ge "$minimum_version"
}

prepare_stage_directory() {
    mkdir -p -- "$INSTALLER_STAGE_ROOT" || return 1
    chown root:root "$INSTALLER_STAGE_ROOT" || return 1
    chmod 0700 "$INSTALLER_STAGE_ROOT" || return 1

    INSTALLER_STAGE_DIR=$(mktemp -d "$INSTALLER_STAGE_ROOT/stage.XXXXXX") \
        || return 1
    INSTALL_TEMP_PATHS+=("$INSTALLER_STAGE_DIR")
    chown root:root "$INSTALLER_STAGE_DIR" || return 1
    chmod 0700 "$INSTALLER_STAGE_DIR" || return 1
}

stage_verified_release() {
    local asset=$1
    local checksum_count=0
    local checksum_line=''
    local checksum_pattern='^([0-9a-f]{64})  (.+)$'
    local downloaded_version
    local expected_asset
    local index
    local key_description
    local key_type
    local line
    local release_url
    local verified_key

    prepare_stage_directory || return 1
    verified_key="$INSTALLER_STAGE_DIR/release-signing-public.pem"
    if ! printf '%s' "$RELEASE_PUBLIC_KEY_BASE64" \
        | openssl base64 -d -A > "$verified_key"; then
        return 1
    fi
    chown root:root "$verified_key" || return 1
    chmod 0600 "$verified_key" || return 1

    if ! key_description=$(openssl pkey \
        -pubin -in "$verified_key" -text -noout); then
        return 1
    fi
    key_type=${key_description%%$'\n'*}
    unset key_description
    [[ $key_type == 'ED25519 Public-Key:' ]] \
        || return 1

    release_url="https://github.com/StealthSurf-VPN/awg-server/releases/download/v$AWG_SERVER_VERSION"
    curl -fsSL "$release_url/SHA256SUMS" -o "$INSTALLER_STAGE_DIR/SHA256SUMS" \
        || return 1
    curl -fsSL "$release_url/SHA256SUMS.sig" \
        -o "$INSTALLER_STAGE_DIR/SHA256SUMS.sig" || return 1

    openssl pkeyutl -verify -rawin -pubin \
        -inkey "$verified_key" \
        -sigfile "$INSTALLER_STAGE_DIR/SHA256SUMS.sig" \
        -in "$INSTALLER_STAGE_DIR/SHA256SUMS" || return 1

    [[ $(tail -c 1 "$INSTALLER_STAGE_DIR/SHA256SUMS" | wc -l | tr -d '[:space:]') == 1 ]] \
        || return 1
    index=0
    while IFS= read -r line; do
        [[ $index -lt ${#EXPECTED_RELEASE_ASSETS[@]} ]] || return 1
        expected_asset=${EXPECTED_RELEASE_ASSETS[index]}
        [[ $line =~ $checksum_pattern ]] || return 1
        [[ ${BASH_REMATCH[2]} == "$expected_asset" ]] || return 1
        if [[ $expected_asset == "$asset" ]]; then
            checksum_count=$((checksum_count + 1))
            checksum_line=$line
        fi
        index=$((index + 1))
    done < "$INSTALLER_STAGE_DIR/SHA256SUMS"
    [[ $index -eq ${#EXPECTED_RELEASE_ASSETS[@]} ]] || return 1
    [[ $checksum_count -eq 1 ]] \
        || return 1

    curl -fsSL "$release_url/$asset" -o "$INSTALLER_STAGE_DIR/$asset" || return 1
    (cd "$INSTALLER_STAGE_DIR" \
        && printf '%s\n' "$checksum_line" | sha256sum --check --strict -) \
        || return 1
    chown root:root "$INSTALLER_STAGE_DIR/$asset" || return 1
    chmod 0755 "$INSTALLER_STAGE_DIR/$asset" || return 1
    if ! downloaded_version=$("$INSTALLER_STAGE_DIR/$asset" version); then
        return 1
    fi
    [[ $downloaded_version == "awg-server $AWG_SERVER_VERSION" ]] \
        || return 1

    INSTALLER_STAGED_BINARY="$INSTALLER_STAGE_DIR/$asset"
}

write_host_configuration() {
    local environment_tmp
    local sysctl_tmp
    local unit_tmp

    mkdir -p -- "$AWG_DATA_DIR" || return 1
    mkdir -p -- "$(dirname "$INSTALLER_ENV_PATH")" \
        "$(dirname "$INSTALLER_SYSCTL_PATH")" \
        "$(dirname "$INSTALLER_UNIT_PATH")" || return 1

    environment_tmp=$(mktemp "$INSTALLER_ENV_PATH.XXXXXX") || return 1
    INSTALL_TEMP_PATHS+=("$environment_tmp")
    if ! render_environment > "$environment_tmp"; then
        rm -f -- "$environment_tmp"
        return 1
    fi
    chown root:root "$environment_tmp" || return 1
    chmod 0600 "$environment_tmp" || return 1
    mv -f -- "$environment_tmp" "$INSTALLER_ENV_PATH" || return 1

    sysctl_tmp=$(mktemp "$INSTALLER_SYSCTL_PATH.XXXXXX") || return 1
    INSTALL_TEMP_PATHS+=("$sysctl_tmp")
    printf '%s\n' 'net.ipv4.ip_forward=1' > "$sysctl_tmp" || return 1
    chown root:root "$sysctl_tmp" || return 1
    chmod 0644 "$sysctl_tmp" || return 1
    mv -f -- "$sysctl_tmp" "$INSTALLER_SYSCTL_PATH" || return 1
    sysctl -w net.ipv4.ip_forward=1 >/dev/null || return 1

    unit_tmp=$(mktemp "$INSTALLER_UNIT_PATH.XXXXXX") || return 1
    INSTALL_TEMP_PATHS+=("$unit_tmp")
    cat > "$unit_tmp" <<'UNIT' || return 1
[Unit]
Description=AmneziaWG Server
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
EnvironmentFile=/etc/awg-server.env
ExecStartPre=/sbin/modprobe amneziawg
ExecStart=/usr/local/bin/awg-server
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
UNIT
    chown root:root "$unit_tmp" || return 1
    chmod 0644 "$unit_tmp" || return 1
    mv -f -- "$unit_tmp" "$INSTALLER_UNIT_PATH" || return 1
}

start_and_verify_service() {
    local deadline
    local health_port=${AWG_HTTP_PORT:-7777}
    local installed_version
    local previous_invocation

    installed_version=$("$INSTALLER_BINARY_PATH" version 2>/dev/null) || return 1
    [[ $installed_version == "awg-server $AWG_SERVER_VERSION" ]] \
        || return 1

    deadline=$(deadline_after_seconds "$INSTALLER_SERVICE_GATE_SECONDS") || return 1
    run_before_deadline "$deadline" systemctl daemon-reload \
        || return 1
    run_before_deadline "$deadline" systemctl enable awg-server.service \
        || return 1
    if ! previous_invocation=$(run_before_deadline "$deadline" systemctl show \
        --property=InvocationID --value awg-server.service); then
        return 1
    fi

    run_before_deadline "$deadline" \
        systemctl start --no-block awg-server.service || return 1
    run_before_deadline "$deadline" \
        systemctl is-enabled --quiet awg-server.service || return 1

    while remaining_milliseconds "$deadline" >/dev/null; do
        if service_healthy_before_deadline \
            "$deadline" "$previous_invocation" "$health_port"; then
            return
        fi
        run_before_deadline "$deadline" sleep 1 || break
    done

    return 1
}

copy_root_only_backup_file() {
    local source_file=$1
    local destination_file=$2

    [[ -e $source_file ]] || return 0
    [[ -f $source_file ]] || return 1
    cp -- "$source_file" "$destination_file" || return 1
    chown root:root "$destination_file" || return 1
    chmod 0600 "$destination_file"
}

create_upgrade_backup() {
    local pending_dir
    local completed_dir

    mkdir -p -- "$INSTALLER_BACKUP_ROOT" || return 1
    chown root:root "$INSTALLER_BACKUP_ROOT" || return 1
    chmod 0700 "$INSTALLER_BACKUP_ROOT" || return 1

    pending_dir=$(mktemp -d "$INSTALLER_BACKUP_ROOT/.pending.XXXXXX") || return 1
    chown root:root "$pending_dir" || {
        rm -rf -- "$pending_dir"
        return 1
    }
    chmod 0700 "$pending_dir" || {
        rm -rf -- "$pending_dir"
        return 1
    }

    if ! copy_root_only_backup_file "$INSTALLER_ENV_PATH" "$pending_dir/environment" \
        || ! copy_root_only_backup_file "$AWG_DATA_DIR/clients.json" "$pending_dir/clients.json" \
        || ! copy_root_only_backup_file "$AWG_DATA_DIR/usage.json" "$pending_dir/usage.json"; then
        rm -rf -- "$pending_dir"
        return 1
    fi

    completed_dir=${pending_dir/.pending./upgrade.}
    mv -- "$pending_dir" "$completed_dir" || return 1
    INSTALLER_BACKUP_DIR=$completed_dir
}

stop_existing_service() {
    local deadline
    local status

    deadline=$(deadline_after_seconds "$INSTALLER_SERVICE_GATE_SECONDS") || return 1
    if run_before_deadline "$deadline" systemctl is-active --quiet awg-server.service; then
        run_before_deadline "$deadline" systemctl stop awg-server.service \
            >/dev/null 2>&1 || return 1
        if run_before_deadline "$deadline" \
            systemctl is-active --quiet awg-server.service; then
            return 1
        else
            status=$?
        fi
        [[ $status -eq 3 || $status -eq 4 ]] || return 1
        return 0
    else
        status=$?
    fi

    [[ $status -eq 3 || $status -eq 4 ]]
}

assert_no_remaining_awg_interfaces() {
    local interfaces

    interfaces=$(ip -o link show type amneziawg 2>/dev/null) || return 1
    [[ -z $interfaces ]]
}

reload_amneziawg_module() {
    modprobe -r amneziawg >/dev/null 2>&1 || return 1
    modprobe amneziawg >/dev/null 2>&1 || return 1
    modinfo amneziawg >/dev/null 2>&1
}

qualify_staged_runtime() {
    [[ -n $INSTALLER_STAGED_BINARY && -x $INSTALLER_STAGED_BINARY ]] || return 1
    "$INSTALLER_STAGED_BINARY" check-runtime >/dev/null 2>&1
}

install_staged_binary() {
    mkdir -p -- "$(dirname "$INSTALLER_BINARY_PATH")" || return 1
    install -o root -g root -m 0755 -- \
        "$INSTALLER_STAGED_BINARY" "$INSTALLER_BINARY_PATH"
}

stop_failed_service() {
    local deadline
    local status

    deadline=$(deadline_after_seconds "$INSTALLER_SERVICE_GATE_SECONDS") || return 1
    run_before_deadline "$deadline" systemctl stop awg-server.service \
        >/dev/null 2>&1 || true
    if run_before_deadline "$deadline" \
        systemctl is-active --quiet awg-server.service; then
        return 1
    else
        status=$?
    fi

    [[ $status -eq 3 || $status -eq 4 ]]
}

report_recovery() {
    local service_stopped=${1:-0}

    if [[ $service_stopped == 1 ]]; then
        printf '%s\n' "$RECOVERY_GUIDANCE" >&2
    else
        printf '%s\n' "$UNCONFIRMED_RECOVERY_GUIDANCE" >&2
    fi
    if [[ -n $INSTALLER_BACKUP_DIR ]]; then
        printf 'Backup retained at: %s\n' "$INSTALLER_BACKUP_DIR" >&2
    fi
}

fail_stopped_pre_replacement() {
    report_recovery 1
    die "$1"
}

fail_after_replacement() {
    if stop_failed_service; then
        report_recovery 1
    else
        report_recovery 0
    fi
    die "$1"
}

install_release_transaction() {
    local asset=$1

    install_amneziawg || die 'could not install current AmneziaWG packages'
    stage_verified_release "$asset" || die 'could not stage a verified awg-server release'
    stop_existing_service || die 'could not stop awg-server.service'
    create_upgrade_backup \
        || fail_stopped_pre_replacement 'could not create a consistent upgrade backup'
    assert_no_remaining_awg_interfaces \
        || fail_stopped_pre_replacement \
            'refusing to unload AmneziaWG while an unknown interface remains'
    reload_amneziawg_module \
        || fail_stopped_pre_replacement 'could not reload the AmneziaWG module'
    qualify_staged_runtime \
        || fail_stopped_pre_replacement 'staged awg-server did not qualify the AWG 3.1 runtime'
    install_staged_binary \
        || fail_after_replacement 'could not install the staged awg-server binary'
    write_host_configuration \
        || fail_after_replacement 'could not install awg-server configuration'
    start_and_verify_service \
        || fail_after_replacement 'new awg-server service did not pass qualification'
}

main() {
    local asset

    (($# == 0)) || die 'arguments are not supported'

    require_supported_host
    if ! asset=$(release_asset_name "$(uname -m)"); then
        die 'supported architectures are x86_64 and aarch64/arm64'
    fi

    resolve_installer_settings
    AWG_SERVER_VERSION=$(resolve_latest_version)

    install_release_transaction "$asset"

    printf 'awg-server %s is installed and healthy\n' "$AWG_SERVER_VERSION"
    printf '%s\n' \
        "WARNING: inbound firewall policy was not configured and no ports were opened or closed; awg-server manages its own NAT/MASQUERADE rule as interfaces are restored or created. Expose required AWG UDP ports and restrict TCP ${AWG_HTTP_PORT:-7777} to the internal network." \
        >&2
}

handle_interruption() {
    exit 1
}

if [[ ${BASH_SOURCE[0]} == "$0" ]]; then
    trap cleanup_temp_paths EXIT
    trap handle_interruption HUP INT TERM
    main "$@"
fi
