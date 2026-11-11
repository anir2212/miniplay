#!/bin/sh
# Starts a disposable PostgreSQL and the real gateway, admin service, migration and
# abuse-scan commands, then drives the admin portal in headless Chrome.
#
# Real: PostgreSQL, gateway, admin-svc, migrate, abuse-scan, Chrome, and the session,
# score and leaderboard handler code. Stand-in: Redis (cmd/portal-check-backend).
# Not running: Kafka, outbox-publisher, stats-worker. The seeded daily_stats rows
# come from calling the SQL aggregate directly.
#
# Usage: scripts/admin_portal_check.sh EVIDENCE_JSON SCREENSHOT_DIR
set -eu

ROOT=$(cd "$(dirname "$0")/.." && pwd)
EVIDENCE=${1:?evidence JSON path}
SHOTS=${2:?screenshot directory}
PG_BIN=${PG_BIN:-/opt/homebrew/opt/postgresql@18/bin}
PG_PORT=${PORTAL_CHECK_PG_PORT:-55434}
GATEWAY_PORT=${PORTAL_CHECK_GATEWAY_PORT:-18080}
ADMIN_PORT=${PORTAL_CHECK_ADMIN_PORT:-18084}
BACKEND_PORT=${PORTAL_CHECK_BACKEND_PORT:-18081}
FLAG_TTL_SECONDS=5
GO=$ROOT/.toolchain/go/bin/go
WORK=$ROOT/.cache/portal-check
export GOCACHE="$ROOT/.cache/go-build" GOMODCACHE="$ROOT/.cache/gomod" GOPATH="$ROOT/.cache/gopath" GOTOOLCHAIN=local

if [ "$PG_PORT" = 5432 ]; then echo "refusing the everyday PostgreSQL port" >&2; exit 2; fi
rm -rf "$WORK"
mkdir -p "$WORK/bin" "$SHOTS" "$(dirname "$EVIDENCE")"
PIDS=""
cleanup() {
  for pid in $PIDS; do kill "$pid" 2>/dev/null || true; done
  [ -f "$WORK/pg/postmaster.pid" ] && LC_ALL=C "$PG_BIN/pg_ctl" -D "$WORK/pg" -m fast -w stop >/dev/null 2>&1 || true
  rm -rf "$WORK"
}
trap cleanup EXIT INT TERM

cd "$ROOT"
for name in gateway admin-svc migrate abuse-scan portal-check-backend; do
  "$GO" build -o "$WORK/bin/$name" "./cmd/$name"
done
(cd web && npm run build >/dev/null)

LC_ALL=C "$PG_BIN/initdb" -D "$WORK/pg" -U miniplay --auth=trust -E UTF8 --locale=C >/dev/null
LC_ALL=C "$PG_BIN/pg_ctl" -D "$WORK/pg" -o "-p $PG_PORT -c listen_addresses=127.0.0.1 -c unix_socket_directories=''" -l "$WORK/pg.log" -w start >/dev/null
"$PG_BIN/createdb" -h 127.0.0.1 -p "$PG_PORT" -U miniplay miniplay_portal
DATABASE_URL="postgres://miniplay@127.0.0.1:$PG_PORT/miniplay_portal?sslmode=disable"
export DATABASE_URL MINIPLAY_FLAG_TTL="${FLAG_TTL_SECONDS}s" MINIPLAY_PORTAL_CHECK=1
sql() { "$PG_BIN/psql" -h 127.0.0.1 -p "$PG_PORT" -U miniplay -d miniplay_portal -Atc "$1"; }

MIGRATION_DIR=db/migrations "$WORK/bin/migrate" 2>&1 | tail -1
"$WORK/bin/portal-check-backend" -seed 40 2>&1 | tail -1
SCAN=$("$WORK/bin/abuse-scan")

# Fail-closed checks on the real binary: it must refuse to start.
NO_TOKEN_EXIT=0; env -u MINIPLAY_ADMIN_TOKEN "$WORK/bin/admin-svc" >/dev/null 2>&1 || NO_TOKEN_EXIT=$?
SHORT_TOKEN_EXIT=0; MINIPLAY_ADMIN_TOKEN=short "$WORK/bin/admin-svc" >/dev/null 2>&1 || SHORT_TOKEN_EXIT=$?
HARNESS_OPT_IN_EXIT=0; MINIPLAY_PORTAL_CHECK=0 "$WORK/bin/portal-check-backend" >/dev/null 2>&1 || HARNESS_OPT_IN_EXIT=$?

ADMIN_TOKEN=$(openssl rand -hex 24)
HMAC_SECRET=$(openssl rand -hex 24)
MINIPLAY_ADMIN_TOKEN=$ADMIN_TOKEN MINIPLAY_HTTP_ADDR=127.0.0.1:$ADMIN_PORT "$WORK/bin/admin-svc" >"$WORK/admin.log" 2>&1 &
ADMIN_PID=$!; PIDS="$PIDS $ADMIN_PID"
MINIPLAY_HMAC_SECRET=$HMAC_SECRET MINIPLAY_HTTP_ADDR=127.0.0.1:$BACKEND_PORT "$WORK/bin/portal-check-backend" >"$WORK/backend.log" 2>&1 &
PIDS="$PIDS $!"
SESSION_URL=http://127.0.0.1:$BACKEND_PORT SCORE_URL=http://127.0.0.1:$BACKEND_PORT LEADERBOARD_URL=http://127.0.0.1:$BACKEND_PORT \
  ADMIN_URL=http://127.0.0.1:$ADMIN_PORT WEB_DIST=web/dist MINIPLAY_HTTP_ADDR=127.0.0.1:$GATEWAY_PORT "$WORK/bin/gateway" >"$WORK/gateway.log" 2>&1 &
PIDS="$PIDS $!"
BASE=http://127.0.0.1:$GATEWAY_PORT
for _ in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
  curl -fs "$BASE/api/health" >/dev/null 2>&1 && curl -fs "http://127.0.0.1:$ADMIN_PORT/health" >/dev/null 2>&1 && curl -fs "http://127.0.0.1:$BACKEND_PORT/api/leaderboards?game=rhythm" >/dev/null 2>&1 && break
  sleep 0.5
done

BROWSER_RESULT=$WORK/browser.json
BROWSER_EXIT=0
(cd web && MINIPLAY_GATEWAY_URL=$BASE MINIPLAY_ADMIN_TOKEN=$ADMIN_TOKEN MINIPLAY_FLAG_TTL_SECONDS=$FLAG_TTL_SECONDS \
  MINIPLAY_E2E_RESULT=$BROWSER_RESULT MINIPLAY_E2E_SHOTS=$SHOTS MINIPLAY_E2E_OUTPUT=$WORK/playwright \
  npx playwright test --config playwright.admin.config.ts) || BROWSER_EXIT=$?

SQL_RUNS=$(sql "SELECT COALESCE(json_agg(row_to_json(r)),'[]') FROM (SELECT user_id,game,score,base_score,multiplier,flag_version FROM scores WHERE user_id LIKE 'browser-%' ORDER BY submitted_at) r")
SQL_AUDIT=$(sql "SELECT COALESCE(json_agg(row_to_json(a)),'[]') FROM (SELECT actor,action,game,before_value,after_value FROM admin_audit ORDER BY id) a")
SQL_FLAGS=$(sql "SELECT json_agg(row_to_json(f)) FROM (SELECT game,double_points,version FROM feature_flags ORDER BY game) f")
SQL_COUNTS=$(sql "SELECT json_build_object('flaggedRuns',(SELECT COUNT(*) FROM flagged_runs),'scores',(SELECT COUNT(*) FROM scores),'outboxPending',(SELECT COUNT(*) FROM event_outbox WHERE published_at IS NULL),'scoresBreakingArithmetic',(SELECT COUNT(*) FROM scores WHERE score<>base_score*multiplier))")
AUDIT_TAMPER=$(sql "UPDATE admin_audit SET actor='tampered'" 2>&1 || true)
PG_VERSION_TEXT=$("$PG_BIN/postgres" --version)
CHROME_VERSION=$("/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" --version 2>/dev/null || echo unknown)

# With the admin service stopped, the gateway must expose nothing.
kill "$ADMIN_PID" 2>/dev/null || true
wait "$ADMIN_PID" 2>/dev/null || true
sleep 0.5
ADMIN_DOWN_STATUS=$(curl -s -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $ADMIN_TOKEN" "$BASE/api/admin/games")

node - "$EVIDENCE" "$BROWSER_RESULT" <<NODE
const fs = require('node:fs');
const [out, browserPath] = process.argv.slice(2);
const browser = fs.existsSync(browserPath) ? JSON.parse(fs.readFileSync(browserPath, 'utf8')) : null;
fs.writeFileSync(out, JSON.stringify({
  check: 'admin portal in headless Chrome on the real gateway',
  ranAt: new Date().toISOString(),
  environment: { postgres: $(printf '%s' "$PG_VERSION_TEXT" | node -e 'process.stdout.write(JSON.stringify(require("fs").readFileSync(0,"utf8")))'), chrome: $(printf '%s' "$CHROME_VERSION" | node -e 'process.stdout.write(JSON.stringify(require("fs").readFileSync(0,"utf8")))'), flagTtlSeconds: $FLAG_TTL_SECONDS },
  realServices: ['PostgreSQL (disposable, loopback)', 'cmd/gateway', 'cmd/admin-svc', 'cmd/migrate', 'cmd/abuse-scan', 'Google Chrome headless', 'session, score and leaderboard handler code'],
  standIns: ['Redis: cmd/portal-check-backend allows every rate-limit check, drops board writes and serves boards from SQL'],
  notRunning: ['Kafka', 'cmd/outbox-publisher', 'cmd/stats-worker', 'cmd/session-svc, cmd/score-svc and cmd/leaderboard-svc binaries (they need Redis at startup)', '/api/boards/live'],
  syntheticData: 'All players are synthetic. 80 seeded runs plus the browser-* runs driven by the test; seeded daily_stats rows come from calling the SQL aggregate directly, not from Kafka.',
  abuseScan: $SCAN,
  failClosed: { adminSvcWithoutTokenExit: $NO_TOKEN_EXIT, adminSvcShortTokenExit: $SHORT_TOKEN_EXIT, harnessWithoutOptInExit: $HARNESS_OPT_IN_EXIT, gatewayStatusWithAdminSvcStopped: $ADMIN_DOWN_STATUS },
  auditTamperAttempt: $(printf '%s' "$AUDIT_TAMPER" | node -e 'process.stdout.write(JSON.stringify(require("fs").readFileSync(0,"utf8")))'),
  playwrightExit: $BROWSER_EXIT,
  browser,
  sql: { runs: $SQL_RUNS, audit: $SQL_AUDIT, flags: $SQL_FLAGS, counts: $SQL_COUNTS },
}, null, 2) + '\n');
NODE
echo "evidence: $EVIDENCE (playwright exit $BROWSER_EXIT, admin down status $ADMIN_DOWN_STATUS)"
exit "$BROWSER_EXIT"
