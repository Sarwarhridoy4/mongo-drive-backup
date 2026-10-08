#!/usr/bin/env bash
set -euo pipefail

run_binary="/tmp/mongo-drive-backup-run-$$"
app_pid=""

cleanup() {
  rm -f "$run_binary"
}

stop_app() {
  if [[ -n "$app_pid" ]] && kill -0 "$app_pid" 2>/dev/null; then
    kill -TERM "$app_pid" 2>/dev/null || true
    wait "$app_pid" 2>/dev/null || true
  fi
  exit 130
}

trap cleanup EXIT
trap stop_app INT TERM

echo "Building MongoDrive backup service..."
go build -trimpath -ldflags='-s -w' -o "$run_binary" ./cmd/backup

"$run_binary" "$@" &
app_pid=$!
wait "$app_pid"
