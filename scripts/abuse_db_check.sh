#!/bin/bash
# Run the frozen synthetic abuse check against one fresh throwaway PostgreSQL.
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
PG_BIN=${PG_BIN:-/opt/homebrew/opt/postgresql@18/bin}
PG_PORT=${MINIPLAY_ABUSE_PG_PORT:-15444}
if ! [[ $PG_PORT =~ ^[0-9]+$ ]] || (( PG_PORT < 1024 || PG_PORT > 65535 )); then
  printf 'MINIPLAY_ABUSE_PG_PORT must be an unprivileged TCP port\n' >&2
  exit 2
fi
WORK=$(mktemp -d "$ROOT/.cache/abuse-db.XXXXXX")
DATA="$WORK/pg"

cleanup() {
  if [ -f "$DATA/postmaster.pid" ]; then
    LC_ALL=C "$PG_BIN/pg_ctl" -D "$DATA" -m fast -w stop >/dev/null 2>&1 || true
  fi
  rm -rf "$WORK"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

cd "$ROOT"
LC_ALL=C "$PG_BIN/initdb" -D "$DATA" -U miniplay --auth=trust -E UTF8 --locale=C >"$WORK/initdb.log"
LC_ALL=C "$PG_BIN/pg_ctl" -D "$DATA" \
  -o "-p $PG_PORT -c listen_addresses=127.0.0.1 -c unix_socket_directories=''" \
  -l "$WORK/pg.log" -w start >"$WORK/start.log"
"$PG_BIN/createdb" -h 127.0.0.1 -p "$PG_PORT" -U miniplay miniplay_abuse_eval

DATABASE_URL="postgres://miniplay@127.0.0.1:$PG_PORT/miniplay_abuse_eval?sslmode=disable" \
GOPATH="$ROOT/.cache/gopath" GOMODCACHE="$ROOT/.cache/gomod" \
GOCACHE="$ROOT/.cache/go-build" GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOPROXY=off \
  "$ROOT/.toolchain/go/bin/go" run ./cmd/abuse-db-eval
