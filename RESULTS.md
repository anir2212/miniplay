# Measured results and acceptance

Results below were collected locally on 2026-10-12, 2026-10-13, 2026-10-15, 2026-10-16 and 2026-10-22. A local load profile was run on 2026-10-22 (below); no GPU or cloud benchmark has been run.

| Check | Result | Command or evidence |
|---|---:|---|
| TypeScript game rules | 2 tests passed | `cd web && npm test` |
| Web build | Passed | `cd web && npm run build` |
| Chrome game-start smoke | 1 test passed, exercises both games | `cd web && npm run test:e2e` |
| Flutter static analysis | 0 issues | `cd flutter_host && flutter analyze` |
| Flutter bridge parser | 2 tests passed | `cd flutter_host && flutter test` |
| Go packages and commands through M3 | Passed, including outbox retry, worker dedupe/dead-letter, and real loopback WebSocket snapshot/update tests | `.toolchain/go/bin/go test ./...` (2026-10-13, before M4 files) |
| Go static checks through M3 | Passed | `.toolchain/go/bin/go vet ./...` (before M4 files) |
| M4 model, migration package, and store tests | Passed | `.toolchain/go/bin/go test ./internal/abuse ./internal/migrate ./internal/store` (2026-10-13) |
| Injected-bot simulation | 22 TP, 1 FP, 58 FN; precision 0.9565, recall 0.275 | `.toolchain/go/bin/go run ./cmd/abuse-eval`, seed 47211; 200 synthetic clean and 80 injected-bot holdout runs (2026-10-13) |
| Full Go suite after M4 | Passed all packages; database integration test skipped without its opt-in URL | `GOPATH="$PWD/.cache/gopath" GOMODCACHE="$PWD/.cache/gomod" GOCACHE="$PWD/.cache/go-build" GOTOOLCHAIN=local .toolchain/go/bin/go test ./...` (2026-10-15; loopback test needed sandbox permission) |
| Full Go vet after M4 | Passed with no diagnostics | Same cache environment, `.toolchain/go/bin/go vet ./...` (2026-10-15) |
| Isolated PostgreSQL 14 integration | Passed migrations 2 then 0; 12 concurrent duplicate-token writes yielded 1 accepted/11 duplicate; outbox and event dedupe passed | `MINIPLAY_TEST_DATABASE_URL='postgres://miniplay@127.0.0.1:15432/miniplay_it?sslmode=disable' ... .toolchain/go/bin/go test ./internal/store -run TestPostgresAttemptAndEventIntegration -count=1 -v` (2026-10-15) |
| Clean PostgreSQL restart | Persisted 2 migration versions, 2 attempts, 1 score, 1 rejection, 2 outbox events, 1 processed event, 1 daily accepted count | Isolated `.cache/pgdata` cluster on loopback port 15432; `pg_ctl` stop/start, then `psql` count query (2026-10-15). Cluster stopped afterward. |
| Gateway and HTTP checks | In-process routes, client fallback/404 boundaries, parameterized JSON session request, and a real loopback gateway → session → score flow passed; the second submission returned 409 | `go test ./cmd/gateway ./internal/httpapi -count=1` with local Go caches and loopback permission (2026-10-15); store and limiter are fakes, no Redis/Kafka |
| Full Go suite after HTTP changes | All package tests and `go vet ./...` passed; opt-in PostgreSQL test skipped without its URL | Local Go caches and loopback permission (2026-10-15) |
| Board repair retry logic | Fake-backed worker tests passed: a Redis failure after one SQL aggregate application leaves the event retryable; replay repairs the board without double-counting, and rejected events do not update a board | `go test ./internal/events ./cmd/stats-worker` (2026-10-15); no broker or Redis process |
| Redis board completeness change | Full Go suite and vet passed. A live Redis 7 test passed partial-key detection, SQL backfill merge, lower-score retry, eviction recovery, and empty-marker invalidation/TTL in 0.03 s | `MINIPLAY_TEST_REDIS_URL=redis://127.0.0.1:16379/15 ... go test ./internal/store -run '^TestRedisBoardRecoveryIntegration$' -count=1 -v` against an isolated `redis:7-alpine` container (2026-10-16). Container stopped and removed. |
| Incomplete-board fallback | Two focused handler cases passed: failed backfill falls back to a complete SQL answer; when SQL also fails, returns 503 without reading the partial Redis board | `go test ./internal/httpapi -run TestLeaderboardBackfillFailureDoesNotServePartialCache -count=1 -v` (2026-10-16); fake dependencies |
| OpenAPI document structure | Parsed five public paths and resolved all 43 local `$ref` targets | Ruby YAML parser and local-reference walk on `api/openapi.yaml` (2026-10-16); no live contract test |

## Physical-phone session, 22 October 2026 (afternoon)

The same debug Flutter host ran on a **Samsung Galaxy S23 (SM-S911B), Android 16, WebView 155**, connected by USB. `adb reverse tcp:18080` let the phone's `127.0.0.1:18080` reach the Mac's gateway, so no Wi-Fi or public network was involved. The APK was built with `MINIPLAY_URL=http://127.0.0.1:18080` and demo user `phone1`, against the same kind of disposable stack. Evidence: [`evidence/phone_session_2026-10-22.json`](evidence/phone_session_2026-10-22.json).

- **Pulse Field.** The project owner played with their own taps. The page showed "Finished rhythm: 130 points", and the host's status bar showed "rhythm: 130 points · taps: 39".
- **Comet Catch.** They then switched games and scored 15 from 12 taps.
- **SQL** recorded both as accepted scores for `phone1` (130 in 10,007 ms; 15 in 10,009 ms), with their outbox events published and processed.
- **Live board.** With the app idle on the Comet Catch board, `cmd/e2e-probe` scored another player at 90 from the Mac (200, then 409 for the replay). The phone's leaderboard showed it above `phone1` without any input except a scroll.
- **Final SQL:** 4 accepted attempts (2 from the phone, 2 scripted from the Mac), all published and processed, with a daily count of 4.

Screenshots were not kept, because they showed a messaging app's chat-head overlay with a personal photo. Limits: one phone over USB to the Mac's loopback, a debug build over plain HTTP, and a demo identity.

## Android emulator session, 22 October 2026 (afternoon)

The Flutter host was built as a debug APK with `MINIPLAY_URL=http://10.0.2.2:18080` and demo user `droid1` (Flutter 3.44.1, Gradle 9.1.0). It ran on a headless Android 14 (API 34) Pixel 6 emulator with WebView 113. The stack was the disposable one used for the load profile, with the gateway serving a fresh `web/dist`. Evidence: [`evidence/android_emulator_2026-10-22.json`](evidence/android_emulator_2026-10-22.json), with two screenshots.

- The WebView loaded the gateway page from the emulator, and the host filled in the player name.
- After tapping Start, 16 taps were sent with `adb input` during the ten seconds and 15 registered. The page showed "Finished rhythm: 100 points". The host's own status bar showed "rhythm: 100 points · taps: 15", so the bridge messages arrived and passed its parser.
- SQL held one accepted attempt and one score (droid1, rhythm, 100, 10,009 ms), one published outbox event, one processed event and a daily count of 1. The REST board and the in-app board both listed droid1 with 100.
- With the app idle, two players were scored from the Mac (180 each). The app's board updated to show them above droid1 without any touch, so its live socket worked through the gateway.
- Plain HTTP loaded because Flutter's Gradle plugin sets `usesCleartextTraffic` in debug builds; the app's own manifests do not allow it.

Limits: one emulator session with injected taps, not a person on a physical phone, over cleartext to the Mac's loopback, with a demo identity.

## Load profile and two fixes, 22 October 2026 (afternoon)

The six services ran natively on the M3 Pro (11 cores, 18 GiB), with a disposable PostgreSQL 18.6 cluster, Redis 7.4.11 and a single-node Kafka 3.9.1 on loopback. Each step started from a new cluster and new containers, and a new per-run secret that was not recorded. `cmd/load-probe` ran on the same Mac. New games started at a fixed rate for 60 seconds. Each game opened the player's live board, started a session, waited the real ten seconds, submitted 12 replayed hits, and timed how long the player's rank took to reach the socket. Fifty idle watchers stayed connected throughout, and the leaderboard was read 10 times a second. Each game used its own user and a synthetic client address from 198.18.0.0/15. Evidence: [`evidence/load_profile_2026-10-22.json`](evidence/load_profile_2026-10-22.json).

| New games a second | Games | Session p50 / p99 | Score p50 / p99 | Rank frame p50 / p95 / p99 | Leaderboard p50 / p99 | leaderboard-svc CPU avg / peak |
|---:|---:|---:|---:|---:|---:|---:|
| 5 | 300 | 1.9 / 11.5 ms | 19.9 / 40.3 ms | 27.1 / 41.7 / 46.5 ms | 4.0 / 7.4 ms | 20% / 34% |
| 10 | 600 | 1.2 / 6.6 ms | 15.7 / 22.6 ms | 24.0 / 30.9 / 32.4 ms | 2.5 / 10.4 ms | 54% / 90% |
| 20 | 1,200 | 1.7 / 9.7 ms | 10.9 / 35.8 ms | 21.9 / 40.0 / 47.0 ms | 9.9 / 24.7 ms | 122% / 179% |
| 40 | 2,400 | 6.3 / 17.1 ms | 34.1 / 53.8 ms | 50.1 / 85.0 / 105.7 ms | 37.2 / 69.1 ms | 168% / 219% |

Every game was accepted, and there were no HTTP or socket errors. After each step SQL held exactly one attempt, score, published outbox event, processed event and daily-aggregate count per game. At 40 a second there were about 420 player sockets plus the 50 watchers. The leaderboard service did most of the work; the gateway peaked at 41% and every other service stayed under 10%. Every board change makes each socket rebuild and send a frame. Updates that arrive while a frame is being built fold into the next one, so watchers received fewer frames than there were changes. The saturation point was not reached.

**Defect 1: one rate-limit bucket for everyone.** The session and score limits keyed on the TCP peer. Behind the gateway that is always the gateway's loopback address, so all players shared one bucket: 30 session starts, then 0.5 a second for everyone together. [`clientip.go`](internal/httpapi/clientip.go) now reads `X-Forwarded-For` only when the peer is a trusted proxy (`MINIPLAY_TRUSTED_PROXIES`, default loopback). It reads from the right, skips trusted hops and uses the first untrusted address. A new test sends two clients through a real reverse proxy: the old code keyed both as `session-ip:127.0.0.1`, the new code keys them separately. Through the gateway, 40 starts from one client still gave 30 accepted and 10 refused with 429.

**Defect 2: two wake-ups per score.** The score service and the stats worker both update the boards for every accepted score, and the update script published a notification each time, even when nothing changed. The script now uses `ZADD GT CH` and publishes only on a change. An opt-in Redis test counted 4 notifications for a new, a replayed, a lower and a higher score with the old script, and 2 with the new one. An A/B at 40 games a second (baseline, fix, baseline, fix, each on a fresh stack) gave:

| At 40 a second | Baseline runs | Fix runs |
|---|---:|---:|
| Rank frame p50 / p99 | 49.9–50.1 / 94.6–111.0 ms | 39.7–40.5 / 72.1–74.6 ms |
| Score p99 | 49.7–56.2 ms | 37.9–40.3 ms |
| Leaderboard p99 | 60.5–69.4 ms | 42.1–45.2 ms |
| leaderboard-svc CPU average | 169–173% | 171–181% |

Both fix runs beat both baseline runs on every p50, p95 and p99 measured. CPU did not fall, because at this load the service still runs flat out, but less of its work is wasted.

Checks: `go build`, `go vet` and `go test ./...` passed, with 3 new `httpapi` tests and 2 new `load-probe` tests. Both opt-in Redis tests passed against a disposable Redis DB 15. Limits: one machine, with the probe sharing its CPU. Profile steps are single runs, and the A/B has two runs per variant. The players are synthetic, with no device or mobile network. This is not a capacity claim for any deployment.

## Combined local stack, 22 October 2026

All six Go services ran against a disposable PostgreSQL 18.6 database, Redis 7.4.11 and a single-node Kafka 3.9.1 broker, all on loopback. `cmd/e2e-probe` drove the public gateway contract ([evidence](evidence/integration_2026-10-22.json)).

| Check | Result |
|---|---|
| Score path | Runs of 180, 100 and 50 points were accepted (200), and each replay returned 409. The live WebSocket frame showed each new score 6–8 ms after submission. |
| Live board after cache loss | With the all-time Redis board deleted and its marker stale, the frame after the next score listed **3 of 3** leaders, matching SQL. The same case listed 1 of 3 on 20 October, before the live handler shared `completeBoard`. |
| Durable records | After three runs: 3 attempts, 3 scores, 3 outbox events published, 3 processed events, daily aggregate 3 accepted / sum 330, consumer lag 0. REST all-time, daily and weekly boards equalled SQL best scores. |
| Abuse cases | Bad user or media type 400, unknown path 404, bad period 400, expired or tampered token 401, invalid taps, score mismatch or short duration 422, resubmitted rejected run 409. |
| Worker crash after SQL, before Redis repair and offset commit | The worker stopped on the Redis timeout with the aggregate applied and one record uncommitted. After restart, the replay did not double-count (4 accepted, 7 processed), repaired the Redis board and committed (lag 0). |
| Redis outage and data loss | While Redis was down, leaderboards were served complete from SQL (200) and sessions returned a controlled 503. After a restart with empty data, boards rebuilt from SQL, and a partial one-score daily key was backfilled to 5 players on the next read. |
| Malformed events | 2 of 2 went to `score.deadletter` with a reason; the worker kept running and processed the next valid event. |
| Real browser | Headless Chrome 155 on the gateway-served page completed a no-tap run ("Finished rhythm: 0 points"). The player appeared on the live board and in SQL and Redis. |
| End state | 9 attempts (6 scores, 3 rejections), 9 outbox events all published, 9 processed, daily 6 / 3 / 520 before the browser run; Redis equalled SQL best scores after every check. |

This is one functional run on one machine with demo users. It is not a load or latency measurement, and no Android emulator was used.

## Pending evidence

- A release build over HTTPS on a real network. (An emulator run and a USB-connected physical-phone run, both debug builds, passed on 22 October; see above.)
- Database-backed cheat injection evaluation (abuse checks so far are request-level).
- The 22 October combined run covered integration, worker restart, Redis loss and dead letters once; it is not repeated under load.
- k6 concurrency and latency measurements with hardware specs; pprof before/after.
- Docker Compose, kind/Helm, and remote CI execution.

The precision and recall above describe only this deterministic synthetic generator. No real-player detection or throughput/latency claim is made.
