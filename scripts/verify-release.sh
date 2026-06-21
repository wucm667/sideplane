#!/bin/sh
# verify-release.sh validates locally built release artifacts in dist/:
#   1. verifies every binary against dist/SHA256SUMS,
#   2. runs each binary's --version,
#   3. starts the server on a temp port, confirms /healthz, then stops it.
#
# It performs no live machine mutation, touches no real config, and cleans up
# after itself. The release matrix is cross-compiled for Linux, so when dist
# holds no binary that can run on this host the functional checks build a
# host-native equivalent from source instead of executing a foreign binary.
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
DIST_DIR=${DIST_DIR:-"$ROOT/dist"}
WORK_DIR=$(mktemp -d "${TMPDIR:-/tmp}/sideplane-verify.XXXXXX")
SERVER_PID=""

cleanup() {
  if [ -n "$SERVER_PID" ]; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  rm -rf "$WORK_DIR"
}
trap cleanup EXIT INT TERM

http_ok() {
  python3 - "$1" <<'PY'
import sys
import urllib.request

with urllib.request.urlopen(sys.argv[1], timeout=1) as resp:
    if resp.status < 200 or resp.status >= 300:
        raise SystemExit(1)
PY
}

pick_port() {
  python3 - <<'PY'
import socket

s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
}

# resolve_bin <cmd> prints the path to a host-runnable binary for ./cmd/<cmd>,
# preferring the matching dist artifact and falling back to a native build.
resolve_bin() {
  cmd=$1
  dist_bin="$DIST_DIR/${cmd}_${HOST}"
  if [ -x "$dist_bin" ]; then
    echo "$dist_bin"
    return 0
  fi
  out="$WORK_DIR/$cmd"
  ( cd "$ROOT" && go build -trimpath -o "$out" "./cmd/$cmd" )
  echo "$out"
}

if [ ! -d "$DIST_DIR" ]; then
  echo "verify-release: $DIST_DIR not found; run 'make release-dist' first" >&2
  exit 1
fi
if [ ! -f "$DIST_DIR/SHA256SUMS" ]; then
  echo "verify-release: $DIST_DIR/SHA256SUMS missing; run 'make release-dist' first" >&2
  exit 1
fi

echo "verify-release: verifying checksums"
if command -v sha256sum >/dev/null 2>&1; then
  ( cd "$DIST_DIR" && sha256sum -c SHA256SUMS )
else
  ( cd "$DIST_DIR" && shasum -a 256 -c SHA256SUMS )
fi

HOST="$(go env GOOS)_$(go env GOARCH)"
SERVER_BIN=$(resolve_bin sideplane-server)
SIDECAR_BIN=$(resolve_bin sideplane-sidecar)
CLI_BIN=$(resolve_bin sideplane)

echo "verify-release: reported versions"
"$SERVER_BIN" --version
"$SIDECAR_BIN" --version
"$CLI_BIN" --version

PORT=$(pick_port)
SERVER_URL="http://127.0.0.1:$PORT"
echo "verify-release: starting server on $SERVER_URL"
"$SERVER_BIN" \
  --addr "127.0.0.1:$PORT" \
  --db "$WORK_DIR/verify.db" \
  --signing-key "$WORK_DIR/signing.key" \
  --operator-token verify-release-token \
  >"$WORK_DIR/server.log" 2>&1 &
SERVER_PID=$!

i=0
while [ "$i" -lt 60 ]; do
  if http_ok "$SERVER_URL/healthz" >/dev/null 2>&1; then
    break
  fi
  i=$((i + 1))
  sleep 0.25
done
if [ "$i" -ge 60 ]; then
  echo "verify-release: server did not serve /healthz in time" >&2
  sed -n '1,120p' "$WORK_DIR/server.log" >&2
  exit 1
fi

echo "verify-release: /healthz OK"
echo "verify-release: all checks passed"
