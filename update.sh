#!/bin/bash

red='\033[0;31m'
green='\033[0;32m'
blue='\033[0;34m'
yellow='\033[0;33m'
plain='\033[0m'

xui_folder="${XUI_MAIN_FOLDER:=/usr/local/x-ui}"
xui_service="${XUI_SERVICE:=/etc/systemd/system}"
xui_repo="${XUI_REPO:=gzjacktang/3x-ui}"
xui_github="https://github.com/${xui_repo}"
xui_raw="https://raw.githubusercontent.com/${xui_repo}"
xui_stage_dir=""

_fail() {
    echo -e "${red}$*${plain}" >&2
    exit 2
}

# The status stays outside the replaced program directory and identifies this run.
xui_update_run_id="${XUI_UPDATE_RUN_ID:-0}"
[[ "${xui_update_run_id}" =~ ^[0-9]+$ ]] || xui_update_run_id="0"
xui_update_status_file="${XUI_UPDATE_STATUS_FILE:-/etc/x-ui/update-status.json}"

_write_update_status() {
    local state="$1" exit_code="$2" status_dir tmp_file
    status_dir="$(dirname "${xui_update_status_file}")"
    mkdir -p "${status_dir}" > /dev/null 2>&1
    tmp_file="${xui_update_status_file}.tmp.$$"
    printf '{"runId":"%s","state":"%s","exitCode":%s,"finishedAt":%s}\n' \
        "${xui_update_run_id}" "${state}" "${exit_code}" "$(date +%s)" > "${tmp_file}" 2> /dev/null
    mv -f "${tmp_file}" "${xui_update_status_file}" > /dev/null 2>&1
}

_report_update_exit() {
    local code=$?
    if [[ "$xui_stage_dir" == "${xui_folder%/*}/.x-ui-update."* && -d "$xui_stage_dir" && ! -L "$xui_stage_dir" ]]; then
        rm -rf -- "${xui_stage_dir}"
    fi
    if [[ "${code}" -eq 0 ]]; then
        _write_update_status "success" "0"
    else
        _write_update_status "failed" "${code}"
    fi
}
trap _report_update_exit EXIT
trap 'exit 143' TERM
trap 'exit 130' INT

[[ $EUID -eq 0 ]] || _fail "Please run this script with root privilege."
[[ "$(uname -s)" == "Linux" ]] || _fail "Only Linux amd64 is supported."

arch() {
    case "$(uname -m)" in
        x86_64 | x64 | amd64) echo amd64 ;;
        *) echo "Only Linux amd64 is supported." >&2; return 1 ;;
    esac
}
arch > /dev/null || exit 2

if [[ -r /etc/os-release ]]; then
    source /etc/os-release
elif [[ -r /usr/lib/os-release ]]; then
    source /usr/lib/os-release
else
    _fail "Cannot identify the operating system."
fi
release=$ID
command -v curl > /dev/null 2>&1 || _fail "curl is required."
echo "The OS release is: $release (amd64)"

# Download through the panel bridge while it is still running. Quoted argv
# preserves proxy URLs and never treats them as command text.
download_release() {
    local proxy_args=()
    [[ -z "${XUI_UPDATE_PROXY:-}" ]] || proxy_args=(--proxy "${XUI_UPDATE_PROXY}")
    curl "${proxy_args[@]}" --retry 5 --retry-delay 3 \
        --connect-timeout 15 --max-time 600 "$@"
}

verify_release_archive() {
    local archive="$1" url="$2" expected actual
    if ! download_release -fLsS -o "${archive}.sha256" "${url}.sha256"; then
        rm -f -- "${archive}.sha256"
        echo "Cannot download the required SHA-256 checksum." >&2
        return 1
    fi
    expected=$(awk 'NR == 1 {print $1}' "${archive}.sha256" | tr 'A-F' 'a-f')
    actual=$(sha256sum "$archive" | awk '{print $1}')
    rm -f -- "${archive}.sha256"
    if [[ ! "$expected" =~ ^[0-9a-f]{64}$ || "$expected" != "$actual" ]]; then
        echo "Checksum mismatch for $(basename "$archive")." >&2
        return 1
    fi
    echo "SHA-256 verified: $actual"
}

gen_random_string() {
    local length="$1"
    openssl rand -base64 $((length * 2)) \
        | tr -dc 'a-zA-Z0-9' \
        | head -c "$length"
}

xui_env_file_path() {
    case "${release}" in
        ubuntu | debian | armbian)
            echo "/etc/default/x-ui"
            ;;
        arch | manjaro | parch | alpine)
            echo "/etc/conf.d/x-ui"
            ;;
        *)
            echo "/etc/sysconfig/x-ui"
            ;;
    esac
}

load_xui_env() {
    local env_file
    env_file="$(xui_env_file_path)"
    if [[ -r "$env_file" ]]; then
        set -a
        # shellcheck disable=SC1090
        source "$env_file"
        set +a
    fi
}

install_base() {
    echo -e "${green}Updating and install dependency packages...${plain}"
    case "${release}" in
        ubuntu | debian | armbian)
            apt-get update > /dev/null 2>&1 && apt-get install -y -q cron curl tar tzdata socat openssl > /dev/null 2>&1
            ;;
        fedora | amzn | virtuozzo | rhel | almalinux | rocky | ol)
            dnf makecache -y > /dev/null 2>&1 && dnf install -y -q cronie curl tar tzdata socat openssl > /dev/null 2>&1
            ;;
        centos)
            if [[ "${VERSION_ID}" =~ ^7 ]]; then
                yum makecache -y > /dev/null 2>&1 && yum install -y -q cronie curl tar tzdata socat openssl > /dev/null 2>&1
            else
                dnf makecache -y > /dev/null 2>&1 && dnf install -y -q cronie curl tar tzdata socat openssl > /dev/null 2>&1
            fi
            ;;
        arch | manjaro | parch)
            pacman -Sy --noconfirm cronie curl tar tzdata socat openssl > /dev/null 2>&1
            ;;
        opensuse-tumbleweed | opensuse-leap)
            zypper refresh > /dev/null 2>&1 && zypper -q install -y cron curl tar timezone socat openssl > /dev/null 2>&1
            ;;
        alpine)
            apk update > /dev/null 2>&1 && apk add dcron curl tar tzdata socat openssl > /dev/null 2>&1
            ;;
        *)
            apt-get update > /dev/null 2>&1 && apt install -y -q cron curl tar tzdata socat openssl > /dev/null 2>&1
            ;;
    esac
}

setup_fail2ban() {
    if [[ -n "${XUI_ENABLE_FAIL2BAN+x}" && "${XUI_ENABLE_FAIL2BAN}" != "true" ]]; then
        echo -e "${yellow}XUI_ENABLE_FAIL2BAN=${XUI_ENABLE_FAIL2BAN}, skipping Fail2ban auto-setup.${plain}"
        return 0
    fi

    if [[ ! -x /usr/bin/x-ui ]]; then
        echo -e "${yellow}x-ui CLI not found; skipping Fail2ban auto-setup.${plain}"
        return 0
    fi

    echo -e "${green}Setting up Fail2ban for the IP Limit feature...${plain}"
    if /usr/bin/x-ui setup-fail2ban; then
        echo -e "${green}Fail2ban setup complete.${plain}"
    else
        echo -e "${yellow}Fail2ban setup did not finish; IP Limit stays disabled until you run 'x-ui' and open the IP Limit menu. Continuing.${plain}"
    fi
    return 0
}

_install_xui_service_unit() {
    local source="$1"
    local source_is_url="$2"
    local dest="${xui_service}/x-ui.service"
    local temp_file="${dest}.tmp.$$"

    rm -f "$temp_file"
    if [[ "$source_is_url" == "true" ]]; then
        download_release -fLRo "$temp_file" "$source" > /dev/null 2>&1
    else
        cp -f "$source" "$temp_file" > /dev/null 2>&1
    fi
    if [[ $? -ne 0 ]]; then
        rm -f "$temp_file"
        return 1
    fi
    if [[ ! -s "$temp_file" ]]; then
        rm -f "$temp_file"
        return 1
    fi
    mv -f "$temp_file" "$dest"
    if [[ $? -ne 0 ]]; then
        rm -f "$temp_file"
        return 1
    fi
    return 0
}

# Run only while the service is stopped; no certificate setup or interactive prompt.
config_after_update() {
    local settings path port cert access_scheme="http"
    if ! settings=$("${xui_folder}/x-ui" setting -show true) ||
        ! path=$(printf '%s\n' "$settings" | awk '
            /^get .* failed, error info:/ { failed = 1 }
            /^hasDefaultCredential: (true|false)$/ { credentials++ }
            /^port: [0-9]+$/ { ports++ }
            /^webBasePath:/ { paths++; sub(/^webBasePath: ?/, ""); value = $0 }
            END {
                if (failed || credentials != 1 || ports != 1 || paths != 1 || (value != "" && value !~ /^\//)) exit 1
                print value
            }'); then
        echo "Unable to read panel settings; existing settings were left unchanged." >&2
        return 1
    fi
    port=$(printf '%s\n' "$settings" | awk '/^port: / {print $2}')
    # Existing short paths are valid; only an unset/root path needs initialization.
    if [[ -z "$path" || "$path" == "/" ]]; then
        path="/$(gen_random_string 18)/"
        "${xui_folder}/x-ui" setting -webBasePath "$path" || return 1
    fi
    cert=$("${xui_folder}/x-ui" setting -getCert true 2>/dev/null | awk -F': ' '/^cert:/ {print $2; exit}')
    [[ -z "$cert" ]] || access_scheme="https"
    echo "Existing certificate settings are preserved."
    echo "Panel access: ${access_scheme}://<your-server>:${port}${path}"
}

# Download, verify and stage everything before stopping the running panel.
update_x-ui() {
    load_xui_env
    [[ "$xui_folder" == /* && "$xui_folder" != "/" && -x "$xui_folder/x-ui" ]] ||
        _fail "A valid installed XUI_MAIN_FOLDER is required."
    local tag_version="${XUI_UPDATE_TAG:-dev-latest}" archive url candidate unit_name script_ref
    [[ "$tag_version" != "dev" ]] || tag_version="dev-latest"
    [[ "$tag_version" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]] || _fail "Invalid release tag."
    xui_stage_dir=$(mktemp -d "${xui_folder%/*}/.x-ui-update.XXXXXX") ||
        _fail "Cannot create the update staging directory."
    archive="$xui_stage_dir/x-ui-linux-amd64.tar.gz"
    url="$xui_github/releases/download/$tag_version/x-ui-linux-amd64.tar.gz"
    download_release -fLsS -o "$archive" "$url" || _fail "Failed to download the panel."
    [[ -s "$archive" ]] || _fail "The downloaded archive is empty."
    verify_release_archive "$archive" "$url" || _fail "Panel archive verification failed."

    # Reject paths that could escape the staging directory before extraction.
    tar -tzf "$archive" > "$xui_stage_dir/archive-files" || _fail "The panel archive is invalid."
    if grep -Eq '(^/|(^|/)\.\.(/|$))' "$xui_stage_dir/archive-files"; then
        _fail "The panel archive contains an unsafe path."
    fi
    tar -xzf "$archive" -C "$xui_stage_dir" || _fail "Cannot extract the panel archive."
    candidate="$xui_stage_dir/x-ui"
    for name in x-ui x-ui.sh bin/xray-linux-amd64; do
        [[ -s "$candidate/$name" && ! -L "$candidate/$name" ]] || _fail "The archive is missing $name."
    done
    if [[ "$release" == "alpine" ]]; then
        unit_name=x-ui.rc
        if [[ ! -s "$candidate/$unit_name" ]]; then
            script_ref="$tag_version"
            [[ "$script_ref" != "dev-latest" ]] || script_ref=main
            download_release -fLsS -o "$candidate/$unit_name" "$xui_raw/$script_ref/$unit_name" ||
                _fail "Cannot download the Alpine service script."
        fi
    else
        case "$release" in
            ubuntu|debian|armbian) unit_name=x-ui.service.debian ;;
            arch|manjaro|parch) unit_name=x-ui.service.arch ;;
            *) unit_name=x-ui.service.rhel ;;
        esac
    fi
    [[ -s "$candidate/$unit_name" ]] || _fail "The archive is missing $unit_name."
    chmod +x "$candidate/x-ui" "$candidate/x-ui.sh" "$candidate/bin/xray-linux-amd64"
    if [[ -f "$candidate/bin/mtg-linux-amd64" ]]; then chmod +x "$candidate/bin/mtg-linux-amd64"; fi
    [[ ! -e "$candidate/bin/config.json" ]] || _fail "The archive must not contain a runtime config.json."
    chown -R root:root "$candidate" && chmod -R go-w "$candidate" || _fail "Cannot secure staged program files."

    echo "All update resources are verified; stopping x-ui."
    if [[ "$release" == "alpine" ]]; then
        rc-service x-ui stop || _fail "Cannot stop x-ui; the installation was not changed."
    else
        systemctl stop x-ui || _fail "Cannot stop x-ui; the installation was not changed."
    fi
    # Copy the new program/resources without removing custom bin files, database
    # stores or certificate paths. The archive does not contain runtime config.json.
    cp -a "$candidate/." "$xui_folder/" || _fail "Cannot install the new program files."
    local cli_temp="/usr/bin/x-ui.tmp.$$"
    if ! cp "$candidate/x-ui.sh" "$cli_temp" || ! chmod 755 "$cli_temp" ||
        ! mv -f "$cli_temp" /usr/bin/x-ui; then
        rm -f -- "$cli_temp"
        _fail "Cannot install the x-ui management script."
    fi
    if [[ -f "$xui_folder/bin/config.json" ]]; then chmod 600 "$xui_folder/bin/config.json"; fi
    # No service starts until all schema and settings work has completed.
    "$xui_folder/x-ui" migrate || _fail "Database migration failed; x-ui remains stopped."
    config_after_update || _fail "Cannot finish panel settings; x-ui remains stopped."
    if [[ "$release" == "alpine" ]]; then
        cp "$candidate/$unit_name" /etc/init.d/x-ui && chmod 755 /etc/init.d/x-ui ||
            _fail "Cannot install the Alpine service script."
        rc-update add x-ui || _fail "Cannot enable x-ui."
        rc-service x-ui start || _fail "Cannot start x-ui."
    else
        _install_xui_service_unit "$candidate/$unit_name" false || _fail "Cannot install the service unit."
        systemctl daemon-reload && systemctl enable x-ui && systemctl start x-ui ||
            _fail "Cannot start x-ui."
    fi
    setup_fail2ban
    echo "x-ui $tag_version update finished."
}

install_base
update_x-ui
