#!/usr/bin/env bash
# Downloads xray-core + geoip.dat for Windows embed (PWDTT WB VPN).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DEST="$ROOT/assets/xray"
mkdir -p "$DEST"

XRAY_VER="${XRAY_VERSION:-26.3.27}"
ZIP="Xray-windows-64.zip"
URL="https://github.com/XTLS/Xray-core/releases/download/v${XRAY_VER}/${ZIP}"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

echo "Fetching xray v${XRAY_VER}..."
curl -fsSL "$URL" -o "$TMP/xray.zip"
unzip -qo "$TMP/xray.zip" -d "$TMP/xray"
cp "$TMP/xray/xray.exe" "$DEST/xray.exe"
cp "$TMP/xray/wintun.dll" "$DEST/wintun.dll"
cp "$TMP/xray/geosite.dat" "$DEST/geosite.dat"
chmod +x "$DEST/xray.exe"

GEOIP_URL="${GEOIP_URL:-https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geoip.dat}"
echo "Fetching geoip.dat..."
curl -fsSL "$GEOIP_URL" -o "$DEST/geoip.dat"

echo "OK: $(ls -lh "$DEST/xray.exe" "$DEST/wintun.dll" "$DEST/geosite.dat" "$DEST/geoip.dat")"
