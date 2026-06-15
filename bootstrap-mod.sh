#!/usr/bin/env bash
# Bootstrap sb-ui as a Saltbox mod role.
#
#   curl -fsSL https://raw.githubusercontent.com/totza2010/sb-ui/master/bootstrap-mod.sh | bash
#
# Run as your Saltbox user (NOT root) — `sb` escalates privileges itself, and
# files under /opt/saltbox_mod must stay owned by that user.
#
# What it does:
#   1. ensures saltbox_mod is installed (sb install saltbox-mod)
#   2. downloads the versioned role tarball from the latest release
#   3. drops it into /opt/saltbox_mod/roles/sbui
#   4. registers it in saltbox_mod.yml
#   5. sb install mod-sbui   (pulls the binary, wires Traefik + SSO + DNS + systemd)
#
# Env overrides: SB_UI_REPO (default totza2010/sb-ui), SALTBOX_MOD_DIR (/opt/saltbox_mod)
set -euo pipefail

REPO="${SB_UI_REPO:-totza2010/sb-ui}"
MOD_DIR="${SALTBOX_MOD_DIR:-/opt/saltbox_mod}"
ROLE="sbui"

if [[ $EUID -eq 0 ]]; then
  echo "Run as your Saltbox user, not root — sb handles privilege escalation." >&2
  exit 1
fi
command -v sb >/dev/null || { echo "sb CLI not found — is this a Saltbox host?" >&2; exit 1; }

# 1. ensure saltbox_mod exists
if [[ ! -f "$MOD_DIR/saltbox_mod.yml" ]]; then
  echo "==> saltbox_mod not found — installing"
  sb install saltbox-mod
fi

# 2. download versioned role tarball
URL="https://github.com/${REPO}/releases/latest/download/sb-ui-role.tar.gz"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
echo "==> Downloading role: $URL"
curl -fSL -o "$TMP/role.tar.gz" "$URL"

# 3. install role (tarball contains the sbui/ directory)
echo "==> Installing role -> $MOD_DIR/roles/$ROLE"
rm -rf "${MOD_DIR:?}/roles/$ROLE"
tar -xzf "$TMP/role.tar.gz" -C "$MOD_DIR/roles"

# 4. register in the playbook if not already present. Insert before the
#    '# Apps End' marker, reusing that line's indentation (keeps YAML valid).
PB="$MOD_DIR/saltbox_mod.yml"
if ! grep -qE "role:[[:space:]]*$ROLE\b" "$PB"; then
  echo "==> Registering '$ROLE' in $PB"
  sed -i "s|^\([[:space:]]*\)# Apps End|\1- { role: $ROLE, tags: ['$ROLE'] }\n\1# Apps End|" "$PB"
fi

# 5. deploy
echo "==> sb install mod-$ROLE"
sb install "mod-$ROLE"

echo
echo "sb-ui is now managed by Saltbox at https://${ROLE}.<your-domain> (behind Authelia)."
echo "Update later: in-UI 'Update' button, or  sb install mod-$ROLE"
