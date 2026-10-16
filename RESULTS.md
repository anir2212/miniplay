# Measured results and acceptance

Results below were collected locally on 2026-10-12, 2026-10-13, 2026-10-15, and 2026-10-16. No load, GPU, or cloud benchmark has been run for MiniPlay.

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

## Pending evidence

- Android emulator WebView run and real browser-to-Go score submission.
- Combined PostgreSQL/Redis/Kafka integration, full HTTP scoring, real worker restart correctness, Redis pub/sub through the live board endpoint, and database-backed cheat injection evaluation. PostgreSQL and Redis checks were separate; the board repair retry uses fakes and no broker.
- k6 concurrency and latency measurements with hardware specs; pprof before/after.
- Docker Compose, kind/Helm, and remote CI execution.

The precision and recall above describe only this deterministic synthetic generator. No real-player detection or throughput/latency claim is made.
