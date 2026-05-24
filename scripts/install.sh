#!/usr/bin/env sh
set -eu

APP_NAME="beacondesk-relay"
REPO="${BEACONDESK_REPO:-poouo/BeaconDesk}"
VERSION="${BEACONDESK_VERSION:-latest}"
CONFIG_DIR="${BEACONDESK_CONFIG_DIR:-/etc/beacondesk}"
LOG_DIR="${BEACONDESK_LOG_DIR:-/var/log/beacondesk}"
BIN_PATH="${BEACONDESK_BIN_PATH:-/usr/local/bin/${APP_NAME}}"
SERVICE_PATH="/etc/systemd/system/beacondesk-relay.service"
ACTION="${1:-install}"

need_root() {
  if [ "$(id -u)" -ne 0 ]; then
    echo "Please run as root." >&2
    exit 1
  fi
}

need_systemd() {
  if ! command -v systemctl >/dev/null 2>&1; then
    echo "systemctl is required." >&2
    exit 1
  fi
}

detect_arch() {
  arch="$(uname -m)"
  case "$arch" in
    x86_64|amd64) echo "linux-amd64" ;;
    aarch64|arm64) echo "linux-arm64" ;;
    armv7l|armv7*) echo "linux-armv7" ;;
    *) echo "Unsupported architecture: $arch" >&2; exit 1 ;;
  esac
}

release_url() {
  asset="$1"
  if [ "$VERSION" = "latest" ]; then
    echo "https://github.com/${REPO}/releases/latest/download/${asset}"
  else
    echo "https://github.com/${REPO}/releases/download/${VERSION}/${asset}"
  fi
}

download_file() {
  url="$1"
  path="$2"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$url" -o "$path"
  elif command -v wget >/dev/null 2>&1; then
    wget -q "$url" -O "$path"
  else
    echo "curl or wget is required." >&2
    exit 1
  fi
}

verify_checksum() {
  file="$1"
  sums="$2"
  asset="$3"
  if ! command -v sha256sum >/dev/null 2>&1; then
    echo "sha256sum is required for release verification." >&2
    exit 1
  fi
  expected="$(awk -v name="$asset" '$2 == name { print $1 }' "$sums")"
  if [ -z "$expected" ]; then
    echo "No checksum entry found for $asset." >&2
    exit 1
  fi
  actual="$(sha256sum "$file" | awk '{ print $1 }')"
  if [ "$expected" != "$actual" ]; then
    echo "Checksum verification failed for $asset." >&2
    exit 1
  fi
}

ensure_user_and_dirs() {
  if ! id beacondesk >/dev/null 2>&1; then
    useradd --system --home "$CONFIG_DIR" --shell /usr/sbin/nologin beacondesk
  fi
  install -d -m 0750 -o beacondesk -g beacondesk "$CONFIG_DIR" "$LOG_DIR" "$CONFIG_DIR/tls"
}

generate_self_signed_tls() {
  cert="$CONFIG_DIR/tls/selfsigned.crt"
  key="$CONFIG_DIR/tls/selfsigned.key"
  if [ -f "$cert" ] && [ -f "$key" ]; then
    return
  fi
  if ! command -v openssl >/dev/null 2>&1; then
    echo "openssl is required to generate default TLS certificates." >&2
    echo "Install openssl or place tls_cert_file/tls_key_file manually in ${CONFIG_DIR}/tls." >&2
    exit 1
  fi
  hostname="$(hostname 2>/dev/null || echo beacondesk-relay)"
  openssl req -x509 -newkey rsa:2048 -sha256 -days 3650 -nodes \
    -keyout "$key" -out "$cert" -subj "/CN=${hostname}" >/dev/null 2>&1
  chown beacondesk:beacondesk "$cert" "$key"
  chmod 0640 "$cert" "$key"
}

write_default_config() {
  if [ -f "$CONFIG_DIR/relay.conf" ]; then
    return
  fi
  generate_self_signed_tls
  cat > "$CONFIG_DIR/relay.conf" <<'EOF'
# BeaconDesk connection server configuration.
# Replace the self-signed certificate with a trusted certificate for production.
listen = ":8443"
transport = "websocket"
websocket_path = "/ws"
web_control_enabled = true
web_control_path = "/web"
# For TCP transport mode only, set a separate web listener for browser control links.
# web_listen = ":8080"
# Set this when serving through a domain or reverse proxy:
# public_base_url = "https://relay.example.com"
shared_token = "change-me"
tls_cert_file = "/etc/beacondesk/tls/selfsigned.crt"
tls_key_file = "/etc/beacondesk/tls/selfsigned.key"
heartbeat_timeout_seconds = 45
max_clients = 1000
bandwidth_limit_kbps = 4096
allow_insecure_plaintext = false
EOF
  chown beacondesk:beacondesk "$CONFIG_DIR/relay.conf"
  chmod 0640 "$CONFIG_DIR/relay.conf"
}

write_service() {
  cat > "$SERVICE_PATH" <<EOF
[Unit]
Description=BeaconDesk Connection Server
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=beacondesk
Group=beacondesk
ExecStart=${BIN_PATH} -config ${CONFIG_DIR}/relay.conf
Restart=on-failure
RestartSec=3
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=full
ProtectHome=true
ReadWritePaths=${CONFIG_DIR} ${LOG_DIR}

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
}

install_binary() {
  target="$(detect_arch)"
  asset="${APP_NAME}-${target}"
  tmp="$(mktemp)"
  sums_tmp="$(mktemp)"
  trap 'rm -f "$tmp" "$sums_tmp"' EXIT
  echo "Downloading ${asset} from ${REPO} (${VERSION})"
  download_file "$(release_url "$asset")" "$tmp"
  download_file "$(release_url "SHA256SUMS")" "$sums_tmp"
  verify_checksum "$tmp" "$sums_tmp" "$asset"
  install -m 0755 "$tmp" "$BIN_PATH"
}

do_install() {
  need_root
  need_systemd
  ensure_user_and_dirs
  install_binary
  write_default_config
  write_service
  systemctl enable beacondesk-relay
  systemctl restart beacondesk-relay
  systemctl status beacondesk-relay --no-pager
}

do_upgrade() {
  need_root
  need_systemd
  install_binary
  write_service
  systemctl restart beacondesk-relay
  systemctl status beacondesk-relay --no-pager
}

do_uninstall() {
  need_root
  need_systemd
  systemctl stop beacondesk-relay 2>/dev/null || true
  systemctl disable beacondesk-relay 2>/dev/null || true
  rm -f "$SERVICE_PATH"
  systemctl daemon-reload
  rm -f "$BIN_PATH"
  echo "BeaconDesk connection server binary and service removed."
  echo "Configuration remains in ${CONFIG_DIR}; remove it manually if no longer needed."
}

case "$ACTION" in
  install) do_install ;;
  upgrade) do_upgrade ;;
  uninstall) do_uninstall ;;
  start|stop|restart|status)
    need_root
    need_systemd
    systemctl "$ACTION" beacondesk-relay
    ;;
  *)
    echo "Usage: $0 [install|upgrade|uninstall|start|stop|restart|status]" >&2
    exit 1
    ;;
esac

