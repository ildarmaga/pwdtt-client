#!/usr/bin/env bash
# Sync vendored relay from local wbstream-wbt checkout (CI builds from third_party/relay).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SRC="${WBSTREAM_RELAY:-$ROOT/../wbstream-wbt/whitelist-bypass/relay}"
DST="$ROOT/third_party/relay"
if [[ ! -d "$SRC" ]]; then
  echo "relay source not found: $SRC" >&2
  echo "set WBSTREAM_RELAY=/path/to/whitelist-bypass/relay" >&2
  exit 1
fi
rsync -a --delete --exclude '.git' "$SRC/" "$DST/"
echo "synced $SRC → $DST"
rg -n 'skip KCP/smux restart|module ' "$DST/go.mod" "$DST/wbstream/session.go" 2>/dev/null | head -5 || true
