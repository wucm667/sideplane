#!/bin/sh
set -eu

ROOT=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
TMPDIR=$(mktemp -d "${TMPDIR:-/tmp}/sideplane-smoke.XXXXXX")
SERVER_PID=""
SIDECAR_PID=""
FAILED=0

cleanup() {
  status=$?
  if [ "$status" -ne 0 ]; then
    FAILED=1
  fi
  if [ -n "$SIDECAR_PID" ]; then
    kill "$SIDECAR_PID" 2>/dev/null || true
    wait "$SIDECAR_PID" 2>/dev/null || true
  fi
  if [ -n "$SERVER_PID" ]; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
  if [ "$FAILED" -eq 1 ]; then
    echo "smoke-readonly failed; logs follow" >&2
    for log in "$TMPDIR"/server.log "$TMPDIR"/sidecar.log; do
      if [ -f "$log" ]; then
        echo "== $log ==" >&2
        sed -n '1,220p' "$log" >&2
      fi
    done
  fi
  rm -rf "$TMPDIR"
  exit "$status"
}
trap cleanup EXIT INT TERM

pick_port() {
  python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
}

http_get() {
  python3 - "$1" <<'PY'
import sys
import urllib.request
with urllib.request.urlopen(sys.argv[1], timeout=1) as resp:
    if resp.status < 200 or resp.status >= 300:
        raise SystemExit(resp.status)
PY
}

PORT=$(pick_port)
SERVER_URL="http://127.0.0.1:$PORT"
OPERATOR_TOKEN="smoke-operator-token"
STATE_PATH="$TMPDIR/sidecar-state.json"
DB_PATH="$TMPDIR/sideplane.db"
SIGNING_KEY="$TMPDIR/signing.key"
HERMES_CONFIG="$TMPDIR/hermes-config.yaml"

cat >"$HERMES_CONFIG" <<'EOF'
model:
  default: smoke-model
  provider: smoke-provider
providers: {}
EOF

cd "$ROOT"

go run ./cmd/sideplane-server \
  --addr "127.0.0.1:$PORT" \
  --db "$DB_PATH" \
  --operator-token "$OPERATOR_TOKEN" \
  --signing-key "$SIGNING_KEY" \
  >"$TMPDIR/server.log" 2>&1 &
SERVER_PID=$!

i=0
while [ "$i" -lt 60 ]; do
  if http_get "$SERVER_URL/healthz" >/dev/null 2>&1; then
    break
  fi
  i=$((i + 1))
  sleep 0.25
done
if [ "$i" -ge 60 ]; then
  echo "server did not become healthy" >&2
  exit 1
fi

TOKEN_OUTPUT=$(go run ./cmd/sideplane enrollment create \
  --server "$SERVER_URL" \
  --operator-token "$OPERATOR_TOKEN" \
  --expires-in 10m)
ENROLL_TOKEN=$(printf '%s\n' "$TOKEN_OUTPUT" | awk '/enrollment token:/ {print $3}')
if [ -z "$ENROLL_TOKEN" ]; then
  echo "failed to parse enrollment token" >&2
  exit 1
fi

go run ./cmd/sideplane-sidecar enroll \
  --server "$SERVER_URL" \
  --token "$ENROLL_TOKEN" \
  --node-id smoke-node \
  --state "$STATE_PATH" \
  >/dev/null

go run ./cmd/sideplane-sidecar \
  --state "$STATE_PATH" \
  --heartbeat-interval 1s \
  --job-poll-interval 1s \
  --hermes-config-paths "$HERMES_CONFIG" \
  --apply-work-dir "$TMPDIR/apply" \
  >"$TMPDIR/sidecar.log" 2>&1 &
SIDECAR_PID=$!

i=0
while [ "$i" -lt 40 ]; do
  if go run ./cmd/sideplane fleet status --server "$SERVER_URL" | grep -q 'smoke-node'; then
    break
  fi
  i=$((i + 1))
  sleep 0.5
done
if [ "$i" -ge 40 ]; then
  echo "sidecar heartbeat did not register smoke-node" >&2
  exit 1
fi

go run ./cmd/sideplane probe smoke-node \
  --server "$SERVER_URL" \
  --operator-token "$OPERATOR_TOKEN" \
  --wait \
  >/dev/null

go run ./cmd/sideplane node inspect smoke-node --server "$SERVER_URL" | grep -q 'smoke-model'

echo "smoke-readonly ok"
