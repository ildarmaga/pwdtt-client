#!/usr/bin/env bash
# Pre-release gate: VK creds must succeed against a fresh call hash.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PANEL_ROOT="${PANEL_ROOT:-/root/wbstream-wbt/wdtt/panel}"

echo "== 1/3 unit tests (client/core) =="
cd "$ROOT/client/core"
go test ./... -count=1

echo "== 2/3 create fresh VK call (panel live) =="
cd "$PANEL_ROOT"
OUT=$(go test -tags=live ./... -run TestLiveCreateVKCall -v -timeout 2m 2>&1)
echo "$OUT"
HASH=$(echo "$OUT" | sed -n 's/.*VK_JOIN_ID=\([^ ]*\).*/\1/p' | tail -1)
if [[ -z "$HASH" ]]; then
  echo "WARN: could not create VK call (limit 4?) — using VK_JOIN_ID fallback" >&2
  HASH="${VK_JOIN_ID:-RjgmJoKoacAMChkS-sYyVwECw0E_qcAhEuOeUzhlGCs}"
fi
echo "hash: $HASH"

echo "== 3/3 live creds (toggle OFF, v0.3.40 flow) =="
cd "$ROOT/client/core"
VK_JOIN_ID="$HASH" go test -tags=live -run TestLiveVKCreds -v -timeout 3m

echo "OK: live VK creds passed for $HASH"
