#!/usr/bin/env bash
# sb-ui installer — downloads the latest release binary, installs it under
# /opt/saltbox-ui, and sets up a systemd service.
#
#   curl -fsSL https://raw.githubusercontent.com/totza2010/sb-ui/master/install.sh | sudo bash
#
# Env overrides:
#   SB_UI_REPO    GitHub repo (default totza2010/sb-ui)
#   SB_UI_VERSION release tag (default: latest)
#   SB_UI_DIR     install dir   (default /opt/saltbox-ui)
#   SB_UI_ADDR    listen addr   (default :8000)
#   SB_UI_USER    service user  (default: the owner of SB_UI_DIR, else current sudo user)
set -euo pipefail

REPO="${SB_UI_REPO:-totza2010/sb-ui}"
VERSION="${SB_UI_VERSION:-latest}"
DIR="${SB_UI_DIR:-/opt/saltbox-ui}"
ADDR="${SB_UI_ADDR:-:8000}"

if [[ $EUID -ne 0 ]]; then
  echo "Please run as root (sudo)." >&2
  exit 1
fi

# --- detect arch -------------------------------------------------------------
case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) echo "Unsupported arch: $(uname -m)" >&2; exit 1 ;;
esac
ASSET="sb-ui-linux-${ARCH}"

# --- resolve version ---------------------------------------------------------
if [[ "$VERSION" == "latest" ]]; then
  URL="https://github.com/${REPO}/releases/latest/download/${ASSET}"
else
  URL="https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}"
fi

# --- service user ------------------------------------------------------------
if [[ -n "${SB_UI_USER:-}" ]]; then
  SVC_USER="$SB_UI_USER"
elif [[ -d "$DIR" ]]; then
  SVC_USER="$(stat -c '%U' "$DIR")"
else
  SVC_USER="${SUDO_USER:-root}"
fi

echo "==> Installing sb-ui ($ASSET, $VERSION) to $DIR (user: $SVC_USER)"
install -d -o "$SVC_USER" -g "$SVC_USER" "$DIR"

# --- download (atomic) -------------------------------------------------------
TMP="$(mktemp)"
trap 'rm -f "$TMP"' EXIT
echo "==> Downloading $URL"
curl -fSL --progress-bar -o "$TMP" "$URL"
install -m 0755 -o "$SVC_USER" -g "$SVC_USER" "$TMP" "$DIR/sb-ui"

# --- systemd unit ------------------------------------------------------------
UNIT=/etc/systemd/system/sb-ui.service
echo "==> Writing $UNIT"
cat >"$UNIT" <<EOF
[Unit]
Description=Saltbox web UI (sb-ui)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$SVC_USER
Group=$SVC_USER
WorkingDirectory=$DIR
EnvironmentFile=-$DIR/.env
Environment=SB_UI_ADDR=$ADDR
ExecStart=$DIR/sb-ui
Restart=on-failure
RestartSec=5
NoNewPrivileges=true
ProtectSystem=full

[Install]
WantedBy=multi-user.target
EOF

echo "==> Enabling + starting service"
systemctl daemon-reload
systemctl enable --now sb-ui.service

echo
echo "sb-ui installed: $("$DIR/sb-ui" --version)"
echo "Listening on $ADDR — open the web UI and run the setup wizard."
echo "Logs:   journalctl -u sb-ui -f"
echo "Status: systemctl status sb-ui"
