#!/usr/bin/env bash
set -Eeuo pipefail

readonly RELEASE_PUBLIC_KEY_BASE64='LS0tLS1CRUdJTiBQVUJMSUMgS0VZLS0tLS0KTUNvd0JRWURLMlZ3QXlFQWQrdGtVUzJlTElwMFlpU1FFdHMwYk9CcmNmcTEvZDBQT0pXd29Udyt1NzA9Ci0tLS0tRU5EIFBVQkxJQyBLRVktLS0tLQo='
readonly LATEST_MANIFEST_URL='https://github.com/StealthSurf-VPN/awg-server/releases/latest/download/SHA256SUMS'
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
)
readonly -a REQUIRED_KEYS=(
    AWG_API_TOKEN
    AWG_ENDPOINT
    AWG_ADDRESS
)

AWG_SERVER_VERSION=''
PROCESS_ENV_PRESENT=()
PROCESS_ENV_VALUES=()
INSTALL_TEMP_PATHS=()

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

load_config_file() {
    local config_file=${1:-}
    local mode

    [[ -f $config_file ]] || die "$config_file must be a regular file"
    [[ -O $config_file ]] || die "$config_file must be owned by the effective user"

    if mode=$(stat -c '%a' -- "$config_file" 2>/dev/null); then
        :
    elif mode=$(stat -f '%Lp' "$config_file" 2>/dev/null); then
        :
    else
        die "cannot inspect permissions for $config_file"
    fi

    [[ $mode =~ ^[0-7]+$ ]] || die "invalid permissions for $config_file"
    (( (8#$mode & 0022) == 0 )) \
        || die "$config_file must not be group or world writable"

    # shellcheck disable=SC1090
    source "$config_file"
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
            die "$key must not contain carriage returns or newlines"
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
        x86_64) printf 'awg-server-linux-amd64\n' ;;
        aarch64 | arm64) printf 'awg-server-linux-arm64\n' ;;
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
    remaining_milliseconds "$deadline" >/dev/null
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
    local tool

    apt-get update
    DEBIAN_FRONTEND=noninteractive apt-get install -y \
        build-essential ca-certificates curl dkms gnupg2 iproute2 iptables \
        libelf-dev openssl python3-launchpadlib software-properties-common \
        "linux-headers-$(uname -r)"

    if [[ $(uname -r) == *xanmod* ]]; then
        install -d -m 0755 /etc/apt/keyrings
        curl -fsSL https://apt.llvm.org/llvm-snapshot.gpg.key \
            | gpg --batch --yes --dearmor \
                -o /etc/apt/keyrings/llvm-snapshot.gpg
        printf '%s\n' \
            'deb [signed-by=/etc/apt/keyrings/llvm-snapshot.gpg] https://apt.llvm.org/jammy/ llvm-toolchain-jammy-19 main' \
            > /etc/apt/sources.list.d/llvm-19.list
        apt-get update
        DEBIAN_FRONTEND=noninteractive apt-get install -y \
            clang-19 lld-19 llvm-19
        for tool in \
            clang clang++ ld.lld llvm-ar llvm-nm llvm-objcopy llvm-objdump \
            llvm-readelf llvm-strip; do
            update-alternatives --install \
                "/usr/bin/$tool" "$tool" "/usr/bin/$tool-19" 190
            update-alternatives --set "$tool" "/usr/bin/$tool-19"
        done
        export PATH="/usr/lib/llvm-19/bin:$PATH"
    fi

    add-apt-repository -y ppa:amnezia/ppa
    apt-get update
    DEBIAN_FRONTEND=noninteractive apt-get install -y amneziawg
    modprobe amneziawg
    modinfo amneziawg >/dev/null
    awg --version
}

install_verified_release() {
    local asset=$1
    local checksum_count=0
    local checksum_line=''
    local checksum_pattern='^([0-9a-f]{64})  (.+)$'
    local downloaded_version
    local key_description
    local key_type
    local line
    local release_dir
    local release_url
    local verified_key

    release_dir=$(mktemp -d)
    INSTALL_TEMP_PATHS+=("$release_dir")
    verified_key="$release_dir/release-signing-public.pem"
    if ! printf '%s' "$RELEASE_PUBLIC_KEY_BASE64" \
        | openssl base64 -d -A > "$verified_key"; then
        die 'could not decode the release public key'
    fi
    chmod 0600 "$verified_key"

    if ! key_description=$(openssl pkey \
        -pubin -in "$verified_key" -text -noout); then
        die 'could not read the release public key'
    fi
    key_type=${key_description%%$'\n'*}
    unset key_description
    [[ $key_type == 'ED25519 Public-Key:' ]] \
        || die 'release public key must be Ed25519'

    release_url="https://github.com/StealthSurf-VPN/awg-server/releases/download/v$AWG_SERVER_VERSION"
    curl -fsSL "$release_url/SHA256SUMS" -o "$release_dir/SHA256SUMS"
    curl -fsSL "$release_url/SHA256SUMS.sig" \
        -o "$release_dir/SHA256SUMS.sig"

    openssl pkeyutl -verify -rawin -pubin \
        -inkey "$verified_key" \
        -sigfile "$release_dir/SHA256SUMS.sig" \
        -in "$release_dir/SHA256SUMS" \
        || die 'release manifest signature verification failed'

    while IFS= read -r line || [[ -n $line ]]; do
        if [[ $line =~ $checksum_pattern ]] \
            && [[ ${BASH_REMATCH[2]} == "$asset" ]]; then
            checksum_count=$((checksum_count + 1))
            checksum_line=$line
        fi
    done < "$release_dir/SHA256SUMS"
    [[ $checksum_count -eq 1 ]] \
        || die "release manifest must contain exactly one checksum for $asset"

    curl -fsSL "$release_url/$asset" -o "$release_dir/$asset"
    (cd "$release_dir" \
        && printf '%s\n' "$checksum_line" | sha256sum --check --strict -) \
        || die 'release asset checksum verification failed'
    chmod 0755 "$release_dir/$asset"
    if ! downloaded_version=$("$release_dir/$asset" version); then
        die 'downloaded release binary did not report its version'
    fi
    [[ $downloaded_version == "awg-server $AWG_SERVER_VERSION" ]] \
        || die 'downloaded release binary version does not match AWG_SERVER_VERSION'

    install -o root -g root -m 0755 \
        "$release_dir/$asset" /usr/local/bin/awg-server

    rm -rf -- "$release_dir"
}

write_host_configuration() {
    local environment_tmp
    local sysctl_tmp
    local unit_tmp

    mkdir -p -- "$AWG_DATA_DIR"

    environment_tmp=$(mktemp /etc/awg-server.env.XXXXXX)
    INSTALL_TEMP_PATHS+=("$environment_tmp")
    render_environment > "$environment_tmp"
    chown root:root "$environment_tmp"
    chmod 0600 "$environment_tmp"
    mv -f -- "$environment_tmp" /etc/awg-server.env

    sysctl_tmp=$(mktemp /etc/sysctl.d/99-awg-server.conf.XXXXXX)
    INSTALL_TEMP_PATHS+=("$sysctl_tmp")
    printf '%s\n' 'net.ipv4.ip_forward=1' > "$sysctl_tmp"
    chown root:root "$sysctl_tmp"
    chmod 0644 "$sysctl_tmp"
    mv -f -- "$sysctl_tmp" /etc/sysctl.d/99-awg-server.conf
    sysctl -w net.ipv4.ip_forward=1 >/dev/null

    unit_tmp=$(mktemp /etc/systemd/system/awg-server.service.XXXXXX)
    INSTALL_TEMP_PATHS+=("$unit_tmp")
    cat > "$unit_tmp" <<'UNIT'
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
    chown root:root "$unit_tmp"
    chmod 0644 "$unit_tmp"
    mv -f -- "$unit_tmp" /etc/systemd/system/awg-server.service
}

service_gate_failed() {
    systemctl --no-pager --full status awg-server.service >&2 || true
    journalctl --no-pager -u awg-server.service -n 50 >&2 || true
    die "$1"
}

start_and_verify_service() {
    local deadline
    local health_port=${AWG_HTTP_PORT:-7777}
    local installed_version
    local previous_invocation

    if ! installed_version=$(/usr/local/bin/awg-server version); then
        service_gate_failed 'installed binary did not report its version'
    fi
    [[ $installed_version == "awg-server $AWG_SERVER_VERSION" ]] \
        || service_gate_failed \
            'installed binary version does not match AWG_SERVER_VERSION'

    deadline=$(deadline_after_seconds 30)
    run_before_deadline "$deadline" systemctl daemon-reload \
        || service_gate_failed 'systemd daemon-reload failed'
    run_before_deadline "$deadline" systemctl enable awg-server.service \
        || service_gate_failed 'could not enable awg-server.service'
    if ! previous_invocation=$(run_before_deadline "$deadline" systemctl show \
        --property=InvocationID --value awg-server.service); then
        service_gate_failed 'could not read the current service invocation'
    fi

    run_before_deadline "$deadline" \
        systemctl restart --no-block awg-server.service \
        || service_gate_failed 'could not start awg-server.service'
    run_before_deadline "$deadline" \
        systemctl is-enabled --quiet awg-server.service \
        || service_gate_failed 'awg-server.service is not enabled'

    while remaining_milliseconds "$deadline" >/dev/null; do
        if service_healthy_before_deadline \
            "$deadline" "$previous_invocation" "$health_port"; then
            return
        fi
        run_before_deadline "$deadline" sleep 1 || break
    done

    service_gate_failed \
        'awg-server.service did not return the expected health response within 30 seconds'
}

main() {
    local asset
    local key

    (($# == 0)) || die 'arguments are not supported'

    require_supported_host
    if ! asset=$(release_asset_name "$(uname -m)"); then
        die 'supported architectures are x86_64 and aarch64/arm64'
    fi

    capture_process_environment
    if [[ -e /etc/awg-server.env ]]; then
        load_config_file /etc/awg-server.env
    fi
    restore_process_environment

    for key in "${REQUIRED_KEYS[@]}"; do
        require_setting "$key"
    done
    select_data_dir /data /var/lib/awg-server
    AWG_SERVER_VERSION=$(resolve_latest_version)

    install_amneziawg
    install_verified_release "$asset"
    write_host_configuration
    start_and_verify_service

    printf 'awg-server %s is installed and healthy\n' "$AWG_SERVER_VERSION"
    printf '%s\n' \
        "WARNING: inbound firewall policy was not configured and no ports were opened or closed; awg-server manages its own NAT/MASQUERADE rule as interfaces are restored or created. Expose required AWG UDP ports and restrict TCP ${AWG_HTTP_PORT:-7777} to the internal network." \
        >&2
}

if [[ ${BASH_SOURCE[0]} == "$0" ]]; then
    trap cleanup_temp_paths EXIT
    main "$@"
fi
