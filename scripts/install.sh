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

main() {
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

    capture_process_environment
    [[ ! -e /etc/awg-server.env ]] || load_config_file /etc/awg-server.env
    [[ -z $config_file ]] || load_config_file "$config_file"
    restore_process_environment

    for key in "${REQUIRED_KEYS[@]}"; do
        require_setting "$key"
    done
    validate_version "$AWG_SERVER_VERSION"
    select_data_dir
}

if [[ ${BASH_SOURCE[0]} == "$0" ]]; then
    main "$@"
fi
