#!/usr/bin/env bash
set -Eeuo pipefail

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
    AWG_SERVER_VERSION
    AWG_RELEASE_PUBLIC_KEY_FILE
    AWG_API_TOKEN
    AWG_ADDRESS
    AWG_ENDPOINT
)
readonly -a CONFIG_KEYS=(
    AWG_SERVER_VERSION
    AWG_RELEASE_PUBLIC_KEY_FILE
    "${ENVIRONMENT_KEYS[@]}"
)

PROCESS_ENV_PRESENT=()
PROCESS_ENV_VALUES=()

die() {
    printf 'install failed: %s\n' "$1" >&2
    exit 1
}

usage() {
    printf 'Usage: %s [--config FILE]\n' "${0##*/}"
}

validate_version() {
    local version=${1:-}

    [[ $version =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] \
        || die 'AWG_SERVER_VERSION must be a stable MAJOR.MINOR.PATCH version'
}

capture_process_environment() {
    local key

    PROCESS_ENV_PRESENT=()
    PROCESS_ENV_VALUES=()
    for key in "${CONFIG_KEYS[@]}"; do
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
    source "$config_file" || die "could not load $config_file"
}

restore_process_environment() {
    local index key

    for ((index = 0; index < ${#CONFIG_KEYS[@]}; index++)); do
        [[ ${PROCESS_ENV_PRESENT[index]:-0} == 1 ]] || continue
        key=${CONFIG_KEYS[index]}
        printf -v "$key" '%s' "${PROCESS_ENV_VALUES[index]}"
        export "$key"
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
    local key

    for key in "${ENVIRONMENT_KEYS[@]}"; do
        [[ ${!key+x} ]] || continue
        printf '%s=' "$key"
        printf '%q\n' "${!key}"
    done
}

select_data_dir() {
    local legacy_dir=${1:-/data}
    local default_dir=${2:-/var/lib/awg-server}

    [[ -n ${AWG_DATA_DIR:-} ]] && return
    if [[ -d $legacy_dir ]]; then
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

health_response_ok() {
    [[ ${1:-} == '{"status":"ok"}' ]]
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

    if systemd-detect-virt --quiet --container; then
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
    trap 'rm -rf -- "$release_dir"' EXIT
    verified_key="$release_dir/release-signing-public.pem"
    install -m 0644 "$AWG_RELEASE_PUBLIC_KEY_FILE" "$verified_key"

    if ! key_description=$(openssl pkey \
        -pubin -in "$verified_key" -text -noout); then
        die 'could not read the release public key'
    fi
    key_type=${key_description%%$'\n'*}
    unset key_description
    [[ $key_type == 'ED25519 Public-Key:' ]] \
        || die 'release public key must be Ed25519'

    release_url="https://github.com/StealthSurf-VPN/awg-server/releases/download/v$AWG_SERVER_VERSION"
    curl -fsSL "$release_url/$asset" -o "$release_dir/$asset"
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

    (cd "$release_dir" \
        && printf '%s\n' "$checksum_line" | sha256sum --check --strict -) \
        || die 'release asset checksum verification failed'
    chmod 0755 "$release_dir/$asset"
    if ! downloaded_version=$("$release_dir/$asset" version); then
        die 'downloaded release binary did not report its version'
    fi
    [[ $downloaded_version == "awg-server $AWG_SERVER_VERSION" ]] \
        || die 'downloaded release binary version does not match AWG_SERVER_VERSION'

    install -d -m 0755 /etc/awg-server
    install -o root -g root -m 0644 \
        "$verified_key" /etc/awg-server/release-signing-public.pem
    install -o root -g root -m 0755 \
        "$release_dir/$asset" /usr/local/bin/awg-server

    rm -rf -- "$release_dir"
    trap - EXIT
}

write_host_configuration() {
    local environment_tmp
    local sysctl_tmp
    local unit_tmp

    mkdir -p -- "$AWG_DATA_DIR"

    environment_tmp=$(mktemp /etc/awg-server.env.XXXXXX)
    trap 'rm -f -- "$environment_tmp"' EXIT
    render_environment > "$environment_tmp"
    chown root:root "$environment_tmp"
    chmod 0600 "$environment_tmp"
    mv -f -- "$environment_tmp" /etc/awg-server.env
    trap - EXIT

    sysctl_tmp=$(mktemp /etc/sysctl.d/99-awg-server.conf.XXXXXX)
    trap 'rm -f -- "$sysctl_tmp"' EXIT
    printf '%s\n' 'net.ipv4.ip_forward=1' > "$sysctl_tmp"
    chown root:root "$sysctl_tmp"
    chmod 0644 "$sysctl_tmp"
    mv -f -- "$sysctl_tmp" /etc/sysctl.d/99-awg-server.conf
    trap - EXIT
    sysctl -w net.ipv4.ip_forward=1 >/dev/null

    unit_tmp=$(mktemp /etc/systemd/system/awg-server.service.XXXXXX)
    trap 'rm -f -- "$unit_tmp"' EXIT
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
    trap - EXIT
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
    local response

    if ! installed_version=$(/usr/local/bin/awg-server version); then
        service_gate_failed 'installed binary did not report its version'
    fi
    [[ $installed_version == "awg-server $AWG_SERVER_VERSION" ]] \
        || service_gate_failed \
            'installed binary version does not match AWG_SERVER_VERSION'

    systemctl daemon-reload \
        || service_gate_failed 'systemd daemon-reload failed'
    systemctl enable awg-server.service \
        || service_gate_failed 'could not enable awg-server.service'
    systemctl restart awg-server.service \
        || service_gate_failed 'could not start awg-server.service'
    systemctl is-enabled --quiet awg-server.service \
        || service_gate_failed 'awg-server.service is not enabled'

    deadline=$((SECONDS + 30))
    while ((SECONDS < deadline)); do
        if systemctl is-active --quiet awg-server.service; then
            response=$(curl -fsS --noproxy '*' --max-time 1 \
                "http://127.0.0.1:$health_port/health" 2>/dev/null || true)
            if health_response_ok "$response"; then
                return
            fi
        fi
        sleep 1
    done

    service_gate_failed \
        'awg-server.service did not return the expected health response within 30 seconds'
}

main() {
    local asset
    local config_file=''
    local key

    while (($#)); do
        case $1 in
            --config)
                (($# >= 2)) || die '--config requires a file'
                config_file=$2
                shift 2
                ;;
            -h | --help)
                usage
                return
                ;;
            *)
                usage >&2
                die "unknown argument: $1"
                ;;
        esac
    done

    require_supported_host
    if ! asset=$(release_asset_name "$(uname -m)"); then
        die 'supported architectures are x86_64 and aarch64/arm64'
    fi

    capture_process_environment
    [[ ! -e /etc/awg-server.env ]] || load_config_file /etc/awg-server.env
    [[ -z $config_file ]] || load_config_file "$config_file"
    restore_process_environment

    if [[ -z ${AWG_RELEASE_PUBLIC_KEY_FILE:-} \
        && -f /etc/awg-server/release-signing-public.pem ]]; then
        AWG_RELEASE_PUBLIC_KEY_FILE=/etc/awg-server/release-signing-public.pem
    fi

    for key in "${REQUIRED_KEYS[@]}"; do
        require_setting "$key"
    done
    validate_version "$AWG_SERVER_VERSION"
    [[ -f $AWG_RELEASE_PUBLIC_KEY_FILE ]] \
        || die 'AWG_RELEASE_PUBLIC_KEY_FILE must be a regular file'
    select_data_dir

    install_amneziawg
    install_verified_release "$asset"
    write_host_configuration
    start_and_verify_service

    printf 'awg-server %s is installed and healthy\n' "$AWG_SERVER_VERSION"
    printf '%s\n' \
        "WARNING: firewall rules were not changed; expose required AWG UDP ports and restrict TCP ${AWG_HTTP_PORT:-7777} to the internal network." \
        >&2
}

if [[ ${BASH_SOURCE[0]} == "$0" ]]; then
    main "$@"
fi
