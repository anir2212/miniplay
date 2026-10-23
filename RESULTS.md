# Measured results and acceptance

Results below were collected locally on 2026-10-12, 2026-10-13, 2026-10-15, 2026-10-16, 2026-10-22, 2026-10-23 and 2026-10-24. Local load profiles were run on 2026-10-22, with a repeat A/B on 2026-10-23 (below); no GPU or cloud benchmark has been run.

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

## HTTPS release build on the Android emulator, 23–24 October 2026

The release builds were made on 23 October, and the device session ran on a fresh disposable stack between 01:12 and 01:16 on 24 October. The workspace's shared CPU lock was held for each build and for the stack and emulator session. Evidence, with no keys, passwords, tokens or secret: [`evidence/android_release_https_2026-10-24.json`](evidence/android_release_https_2026-10-24.json), with four screenshots in `evidence/android_release_2026-10-24/`.

**What changed first.**
- *The gateway can serve HTTPS.* With `MINIPLAY_TLS_CERT` and `MINIPLAY_TLS_KEY` set, it serves TLS 1.2 or later only ([`internal/config/config.go`](internal/config/config.go), [`cmd/gateway/main.go`](cmd/gateway/main.go)). The services behind it stay plain HTTP on loopback.
- *The release app no longer allows cleartext.* Since M1 the app's main manifest had `android:usesCleartextTraffic="true"`, so every build, release included, allowed plain HTTP. The 22 October notes said Flutter's Gradle plugin added it to debug builds only; that was wrong. Each build type now has a network security config:
  - debug allows cleartext;
  - release and profile refuse cleartext and trust system CAs only;
  - a new non-production `localRelease` build type also trusts one local CA, only for `10.0.2.2`, `127.0.0.1` and `localhost`.
- *Release signing reads an upload key from outside the repository.* Gradle takes it from an untracked properties file or environment variables, and a release task without one stops instead of signing with the debug key. The host now cancels any certificate error and shows it.

**Setup.** OpenSSL made a local CA and a gateway certificate (IP 10.0.2.2 and 127.0.0.1, DNS localhost) in the run's scratch folder. `keytool` made an RSA 3072 upload key there. Both builds used `MINIPLAY_URL=https://10.0.2.2:18443` and user `tls1`, with Flutter 3.44.1, Gradle 9.1.0, Android Gradle plugin 9.0.1 and NDK 28.2. The stack was PostgreSQL 14.18 (Homebrew binaries) on 127.0.0.1:55432, `redis:7.4-alpine` and `apache/kafka:3.9.1` containers, and the six Go services, with the gateway on 127.0.0.1:18443 serving TLS and a fresh `web/dist`. The emulator was the headless Android 14 Pixel 6 AVD with WebView 113.

| Build | Command | APK (arm64-v8a) | Debuggable | Trusts |
|---|---|---:|---|---|
| Production release | `flutter build apk --release --target-platform android-arm64 --dart-define=...` | 15,575,672 bytes | No | System CAs only, no cleartext |
| `localRelease` | `./gradlew ... -Pminiplay.localCaCert=<ca.pem> assembleLocalRelease` with Flutter's own Gradle properties | 17,631,270 bytes | No | System CAs, plus the local CA for three hosts; no cleartext |

Both APKs have the same signer (certificate SHA-256 `f39f615d…e402cb`, a local key that is not committed) and verify with APK Signature Scheme v2. For both, `apkanalyzer manifest debuggable` printed `false`; on the emulator their package flags had no `DEBUGGABLE`, and `run-as` said "package not debuggable". `localRelease` has R8 off: its first build failed in R8 because Flutter's Gradle plugin copies a custom build type's minify setting to the WebView plugin library, which then lost a class the app needs. The build also refused four bad inputs: a release task with no signing configuration, `localRelease` with no CA, the CA's private key in place of its certificate, and a server certificate in place of a CA.

**Device session.**
- The `localRelease` app loaded `https://10.0.2.2:18443/`, and the host filled in the player name.
- After tapping Start, 16 taps were sent with `adb input`, and 15 registered. The page showed "Finished rhythm: 90 points", and the host's bar showed "rhythm: 90 points · taps: 15".
- SQL then held one accepted attempt and one score (tls1, rhythm, 90, 10,039 ms), one published outbox event, one processed event and a daily count of 1. The REST board over HTTPS and the in-app board listed tls1 with 90.
- With the app idle, `cmd/e2e-probe -ca` scored two players at 180 through the HTTPS gateway: 200, then 409 for each replay. The app's board showed both above tls1 without any touch. The page is HTTPS, so its live socket was `wss`.
- Final SQL: 3 accepted attempts, 3 scores, 3 published outbox events, 3 processed events, and a daily count of 3 with a sum of 450. The Redis board matched.
- **Negative check.** The production release app, pointed at the same gateway, showed "Could not load games: certificate rejected (The certificate authority is not trusted.)". The page stayed blank. Logcat had "Trust anchor for certification path not found" and `net_error -202`, and the gateway logged `remote error: tls: unknown certificate`, its only error that session. SQL was unchanged. The same check on 23 October, with only the gateway running, gave the same result.

On 23 October the first `localRelease` launch, seconds after the emulator booted, showed `net::ERR_ADDRESS_UNREACHABLE`; a relaunch a minute later loaded the page. Treat that as the emulator's network starting up rather than a TLS fault; it was not investigated further.

Checks: `go build`, `go vet` and gofmt are clean. `go test ./...` passes 38 top-level tests (32 before), with the 4 opt-in tests skipped. The new tests are 4 in `internal/config` (both-or-neither variables, missing files, a trusted and an untrusted client, TLS 1.1 and plain HTTP refused, HTTP/2 offered, and a `wss` upgrade), 1 in `cmd/gateway` (same-origin `wss` through the proxy over TLS, cross-origin refused) and 1 in `cmd/e2e-probe`. `flutter analyze` reports no issues, `flutter test` passes 2 tests, and the web tests (2) and build pass.

Limits: emulator only, with taps injected by `adb`. The gateway was on the Mac's loopback, reached through the emulator alias `10.0.2.2`, with a locally issued certificate; there was no real network, public DNS name or publicly trusted certificate. The game ran in `localRelease`, which trusts the local CA and has R8 off. The production build was only checked for refusal, and it has not loaded a page, because no publicly trusted certificate was available. That it ignores user-installed CAs is shown by its packaged configuration, not by installing one in the emulator. The identity is a demo identity. PostgreSQL 14.18 binaries were used, where the 22 October sessions used 18.6.

## Live-board saturation and two more fixes, 22–23 October 2026

This continues the afternoon profile (described further down) past 40 new games a second. The setup was the same, with these changes. Every run started from a new disposable PostgreSQL 18.6 cluster and new Redis 7.4.11 and Kafka 3.9.1 containers, and no other heavy job ran on the machine during it. Binaries were built with `-trimpath -buildvcs=false`, so a variant's binaries differ only where its code differs. The probe now also records how late each scheduled game starts (`start_lag`). CPU is cumulative CPU time over the probe's run divided by wall time, so 100% is one of the 11 cores. Docker Desktop's own processes were sampled too, because Redis and Kafka sit behind its port forwarding. Evidence, with no tokens or secret: [`evidence/load_saturation_2026-10-22.json`](evidence/load_saturation_2026-10-22.json).

Each step ran:

```sh
load-probe -gateway http://127.0.0.1:18080 -rate R -duration 60s -watchers 50 -board-rate 10 -prefix <per run>
```

The probe kept its schedule in every run: start-lag p99 stayed at or below 10 ms. In every run, SQL's attempts, scores, published and processed events and daily count agreed with each other and with the Redis board. Up to 160 a second they also equalled the games the probe saw accepted. From 200 a second, SQL held up to about 150 more, because those requests committed after the probe's 10-second client timeout.

**Before the fixes (the previous version of the code), 22 October, evening.**

| New games a second | Games | Accepted | Rank frame p50 / p95 / p99 | Score p99 | leaderboard-svc CPU | Docker port forwarding / VM CPU | Redis CPU |
|---:|---:|---:|---:|---:|---:|---:|---:|
| 40 | 2,400 | 2,400 | 42 / 80 / 227 ms | 134 ms | 155% | 177% / 223% | 41% |
| 80 | 4,800 | 4,800 | 143 / 311 / 447 ms | 215 ms | 152% | 174% / 227% | 54% |
| 120 | 7,201 | 5,913 | 320 / 921 / 1,150 ms | 561 ms | 131% | 149% / 212% | 47% |
| 160 | 9,600 | 5,356 | 838 / 3,160 / 5,762 ms | 3,905 ms | 112% | 134% / 182% | 40% |

The 40 a second step repeats the afternoon's last step. Every game was accepted, with p95 close to the afternoon's (80 against 85 ms), but p99 was 227 ms against 72–75 ms; the machine was shared with other work (load averages are in the evidence). Between 40 and 80 the median rank-frame time tripled, still with no errors. From 120 a second games failed: 625 live sockets could not connect, and 663 session and score requests got 502 from the gateway, whose log said `connect: can't assign requested address`.

Two causes showed in the measurements:

- **Every socket rebuilt its own frame.** After each board change, every live socket on that board ran the completeness check, the top-20 read and its rank lookup, so one change cost three Redis round trips per open socket. At 40 a second Redis served 2.9 million commands during the run (about 41,000 a second) to about 600 client connections, one subscription per socket. Each round trip passed through Docker Desktop's port forwarding, which used 175–195% CPU while Redis itself used 40–56%. As the rate rose, Redis throughput fell: about 51,000 operations a second at 40 a second, 28,000 at 80 and 7,000 at 120. Each socket then folded more changes into each frame (the 50 watchers received 92,000 frames at 40 a second and 33,000 at 80), and the player's rank waited longer. A CPU profile of leaderboard-svc at 80 a second (a separate run with a temporary profiling build) showed about 160% CPU and 4,510 goroutines: 44% of samples were in Redis connection reads, 27% in the per-socket board rebuild, and 7% writing WebSocket frames.
- **Every socket cost three loopback connections:** probe to gateway, gateway to leaderboard, and its own Redis subscription. Every closed connection leaves a port in TIME_WAIT for 30 seconds on macOS. At 120 a second netstat counted 12,600 TIME_WAIT sockets, and macOS has 16,384 ephemeral ports.

**Fix 1: one broadcaster per board.** [`broadcast.go`](internal/httpapi/broadcast.go) gives all sockets on one board in a process a single Redis subscription. After a change, it runs the completeness check and top-20 read once and looks up every watching player's rank in one pipelined request (`store.Redis.Ranks`). It encodes the leaders once and hands each socket its frame. A socket that is slow to write keeps only the newest frame instead of holding up the others. Initial frames, the REST route and the frame JSON are unchanged.

| Broadcaster only, 22 October | Games | Accepted | Rank frame p50 / p95 / p99 | Score p99 | leaderboard-svc CPU | Gateway CPU | Docker port forwarding / VM CPU | Redis CPU |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 40 | 2,400 | 2,400 | 10 / 15 / 25 ms | 15 ms | 33% | 46% | 18% / 57% | 6% |
| 80 | 4,800 | 4,800 | 36 / 59 / 68 ms | 55 ms | 85% | 111% | 24% / 71% | 8% |
| 120 | 7,201 | 7,201 | 65 / 100 / 142 ms | 115 ms | 88% | 112% | 25% / 73% | 8% |
| 160 | 9,600 | 7,752 | 109 / 169 / 190 ms | 170 ms | 77% | 102% | 26% / 79% | 8% |
| 200 | 12,000 | 6,118 | 571 / 9,857 / 9,986 ms | 9,966 ms | 65% | 89% | 25% / 73% | 7% |

Gateway and probe CPU rose because far more frames now reached the clients: the watchers received 158,000–162,000 frames at 80 a second instead of 33,000. Redis held 64–125 client connections instead of 600–1,380. The profile at 80 a second showed about 94% CPU and 1,755 goroutines, with 77% of samples writing WebSocket frames and 3% building boards. An A/B at 80 a second (base, broadcaster, base, broadcaster, each on a fresh stack; only the httpapi and store packages differ) gave:

| At 80 a second, 22 October | Base runs | Broadcaster runs |
|---|---:|---:|
| Rank frame p50 / p95 / p99 | 136–145 / 287–306 / 372–400 ms | 35–36 / 60–63 / 72–87 ms |
| Score p99 | 147–160 ms | 57–70 ms |
| Leaderboard read p99 | 286–302 ms | 30–38 ms |
| leaderboard-svc CPU | 155–161% | 85% |
| Docker port forwarding / VM CPU | 189–195% / 233–235% | 23% / 71% |
| Redis CPU / client connections | 52–56% / 1,044–1,047 | 7–8% / 70–72 |

Both broadcaster runs beat both base runs on every p50, p95 and p99 measured.

**Fix 2: the gateway reused too few upstream connections.** With the broadcaster, 160 a second failed again with `can't assign requested address`, and netstat now showed about 2,500 TIME_WAIT sockets to score-svc and 2,250 to session-svc. `httputil.NewSingleHostReverseProxy` uses `http.DefaultTransport`, which keeps two idle connections per host, so under concurrency most proxied requests opened a new connection and then discarded it. [`cmd/gateway/main.go`](cmd/gateway/main.go) now gives its proxies one transport that keeps up to 256 idle connections per service. A new test sends three rounds of 16 concurrent requests through the proxy: the default transport opened 44 upstream connections, the new one 16. An A/B at 160 a second (broadcaster, plus pool, broadcaster, plus pool; only the gateway binary differs) gave:

| At 160 a second, 22 October | Broadcaster runs | Broadcaster and gateway pool runs |
|---|---:|---:|
| Games accepted of 9,600 | 7,860 and 7,671 | 9,600 and 9,600 |
| Gateway 502s / sockets that could not connect | 776–866 / 964–1,064 | 0 / 0 |
| Rank frame p50 / p95 / p99 | 109–123 / 177–212 / 261–291 ms | 93–98 / 151–154 / 172–204 ms |
| TIME_WAIT to score-svc and session-svc | about 4,800 | 1 or none |

**Where it stops now.** With both fixes, 200, 240 and 280 games a second accepted 7,656 of 12,000, 6,051 of 14,401 and 4,913 of 16,801. The errors were `can't assign requested address` again, on the live sockets and on score-svc's new PostgreSQL connections, plus requests the probe gave up on after 10 seconds. Each game holds two loopback connections (probe to gateway, gateway to leaderboard) for its 10.5 seconds, then leaves both in TIME_WAIT for 30 seconds. At 200 a second that needs about 200 × 2 × 40.5 ≈ 16,200 ports, the whole range. The machine's CPU was not the limit. At 200 a second leaderboard-svc averaged 75%, the gateway 97%, the probe 74%, Docker 94% and Redis 7%: about 3.5 of 11 cores for everything measured. So on this laptop the live board kept up through 160 new games a second, about 1,700 player sockets plus 50 watchers, with every game accepted and the rank frame at p99 172–204 ms. Above that, the machine's ephemeral port range stops this one-host test first.

The next limit inside the code is already visible. Each board's hub builds frames one at a time, and a build does one rank lookup and one frame per watching player, so it takes longer as players join. Changes that arrive during a build fold into the next one. Each watcher received about one frame per change at 40 a second, one per 1.5 changes at 80, one per 3.1 at 120 and one per 5.5 at 160 with both fixes. The rank frame's p99 rose with that, from 68–87 ms at 80 a second to 172–204 ms at 160.

**Repeat A/B, 23 October, morning.** Base (the previous version) against the final code (broadcaster and gateway pool) at 80 a second, in the order base, fix, base, fix. Each run had a fresh stack. The binaries were rebuilt from the previous version and from the final tree, and were byte-identical to the 22 October builds. The laptop was on battery (29% falling to 25%) with Low Power Mode on, and each run was wrapped in `caffeinate -i`.

| At 80 a second, 23 October | Base runs | Final runs |
|---|---:|---:|
| Games accepted, errors | 4,800 of 4,800, none | 4,800 of 4,800, none |
| Rank frame p50 / p95 / p99 | 146 / 302 / 382–391 ms | 35–36 / 59 / 68–70 ms |
| Score p99 | 164–186 ms | 54–55 ms |
| Leaderboard read p99 | 277–293 ms | 29–30 ms |
| leaderboard-svc CPU | 161–163% | 85–86% |
| Docker port forwarding / VM CPU | 196–198% / 228–231% | 25% / 71% |
| Redis CPU / client connections | 48–50% / 1,041–1,042 | 7–8% / 70–75 |

Both final runs beat both base runs on every p50, p95 and p99, and the numbers match the 22 October A/B closely. An earlier set that morning was discarded whole. During its fourth run the laptop went to sleep (it woke at 08:57:28), so 201 score requests arrived after their 120-second run tokens had expired and got 401. Its first three runs were normal.

Checks: `go build`, `go vet` and gofmt are clean. `go test ./...` passes 32 top-level tests (26 before), with the 4 opt-in database and Redis tests skipped. The new tests are 5 broadcaster tests in `internal/httpapi`, 1 gateway connection test and 1 opt-in Redis test of `Ranks`. On 22 October the three opt-in Redis tests passed against a disposable Redis 7.4.11 DB 15, which was empty afterwards.

Limits: one laptop with synthetic players, everything on loopback, and the probe sharing the CPU. Redis and Kafka ran behind Docker Desktop's port forwarding, which made each base-version round trip more expensive than it would be on a Linux host. Even so, the base version's round trips per change still grow with the number of open sockets. Steps are single runs, and each A/B has two runs per variant. Other work shared the machine. This is not a capacity claim for any deployment.

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
- Plain HTTP loaded because the merged manifest had `usesCleartextTraffic=true`. (Corrected 23 October: this line first said Flutter's Gradle plugin adds it to debug builds only. It came from the app's own main manifest, so release builds allowed cleartext too until the 23 October change above.)

Limits: one emulator session with injected taps, not a person on a physical phone, over cleartext to the Mac's loopback, with a demo identity.

## Load profile and two fixes, 22 October 2026 (afternoon)

The six services ran natively on the M3 Pro (11 cores, 18 GiB), with a disposable PostgreSQL 18.6 cluster, Redis 7.4.11 and a single-node Kafka 3.9.1 on loopback. Each step started from a new cluster and new containers, and a new per-run secret that was not recorded. `cmd/load-probe` ran on the same Mac. New games started at a fixed rate for 60 seconds. Each game opened the player's live board, started a session, waited the real ten seconds, submitted 12 replayed hits, and timed how long the player's rank took to reach the socket. Fifty idle watchers stayed connected throughout, and the leaderboard was read 10 times a second. Each game used its own user and a synthetic client address from 198.18.0.0/15. Evidence: [`evidence/load_profile_2026-10-22.json`](evidence/load_profile_2026-10-22.json).

| New games a second | Games | Session p50 / p99 | Score p50 / p99 | Rank frame p50 / p95 / p99 | Leaderboard p50 / p99 | leaderboard-svc CPU avg / peak |
|---:|---:|---:|---:|---:|---:|---:|
| 5 | 300 | 1.9 / 11.5 ms | 19.9 / 40.3 ms | 27.1 / 41.7 / 46.5 ms | 4.0 / 7.4 ms | 20% / 34% |
| 10 | 600 | 1.2 / 6.6 ms | 15.7 / 22.6 ms | 24.0 / 30.9 / 32.4 ms | 2.5 / 10.4 ms | 54% / 90% |
| 20 | 1,200 | 1.7 / 9.7 ms | 10.9 / 35.8 ms | 21.9 / 40.0 / 47.0 ms | 9.9 / 24.7 ms | 122% / 179% |
| 40 | 2,400 | 6.3 / 17.1 ms | 34.1 / 53.8 ms | 50.1 / 85.0 / 105.7 ms | 37.2 / 69.1 ms | 168% / 219% |

Every game was accepted, and there were no HTTP or socket errors. After each step SQL held exactly one attempt, score, published outbox event, processed event and daily-aggregate count per game. At 40 a second there were about 420 player sockets plus the 50 watchers. The leaderboard service did most of the work; the gateway peaked at 41% and every other service stayed under 10%. Every board change makes each socket rebuild and send a frame. Updates that arrive while a frame is being built fold into the next one, so watchers received fewer frames than there were changes. The saturation point was not reached that afternoon; the evening runs above found it and changed this design.

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

- The production release build over a real network, against a gateway with a publicly trusted certificate. (Debug builds passed on an emulator and a USB-connected phone on 22 October, and a release-signed `localRelease` build passed over HTTPS with a local CA on the emulator on 24 October; see above.)
- Database-backed cheat injection evaluation (abuse checks so far are request-level).
- The 22 October combined run covered integration, worker restart, Redis loss and dead letters once; it is not repeated under load.
- A load run from more than one machine, and with Redis outside Docker Desktop's port forwarding. The one-host probe runs out of ephemeral ports near 200 new games a second (22 October), so it cannot show where the services themselves stop. The before-and-after CPU profiles so far are of leaderboard-svc at 80 a second only.
- Docker Compose, kind/Helm, and remote CI execution.

The precision and recall above describe only this deterministic synthetic generator. No real-player detection claim is made, and no throughput or latency claim beyond the one-laptop measurements above.
