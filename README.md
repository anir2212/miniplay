# MiniPlay

Small games are easy to put on a page; trustworthy scores and live leaderboards are harder. MiniPlay explores that problem with two original ten-second Phaser games, **Pulse Field** and **Comet Catch**. A Go service signs each run and replays submitted taps before accepting a score. PostgreSQL keeps the durable record, Redis serves rankings, Kafka carries score events, and a Flutter host embeds the web client.

The server can reject a score that does not match the submitted taps. It cannot prove a human played: a bot may fabricate plausible input. The abuse scanner therefore produces review candidates rather than automatic bans.

## What has been checked

- The web game rules, build, and desktop Chrome game-start smoke passed; Flutter analysis and bridge parser tests passed.
- In an isolated PostgreSQL test, 12 concurrent submissions of one run ID produced 1 accepted write and 11 duplicates. The records survived a clean database restart.
- An isolated Redis 7 test exercised partial-board detection, SQL backfill, score retry, eviction recovery, and empty-board handling. Gateway → session → score was also checked over loopback with fake storage. These were separate tests.
- **Combined stack, 22 October.** All six Go services ran against disposable PostgreSQL, Redis and Kafka on loopback. A score went through the gateway, PostgreSQL, the outbox, Kafka, the worker, Redis and a live WebSocket frame. A worker crash between the SQL update and the Redis repair replayed without double-counting. Leaderboards stayed correct from SQL during a Redis outage and rebuilt after a wipe, and malformed events went to the dead-letter topic ([evidence](evidence/integration_2026-10-22.json)).
- **Load profile.** `cmd/load-probe` ran 5, 10, 20 and 40 new games a second through the gateway, each step on a fresh stack. All 4,500 games were accepted and reached SQL and the player's live socket with no errors. At 40 a second (about 420 player sockets plus 50 watchers) the player's rank reached their socket at p99 106 ms, and the leaderboard service was the busiest process. The profile found and fixed two defects:
  - every player behind the gateway shared one rate-limit bucket (limits now use `X-Forwarded-For` from trusted proxies only, set by `MINIPLAY_TRUSTED_PROXIES`);
  - every accepted score woke each live socket twice.

  See the [evidence](evidence/load_profile_2026-10-22.json). It is one machine with synthetic players, not a capacity claim.
- **Live-board saturation, 22–23 October.** Pushed past 40 new games a second, the old code slowed sharply between 40 and 80 and failed games from 120. Every live socket rebuilt its own frame from Redis after each change, and every socket and proxied request used up local ports. Two fixes followed:
  - one broadcaster per board, which reads each change once and pushes it to every socket on that board;
  - a pooled gateway transport.

  At 80 a second the rank frame's p99 fell from 372–400 ms to 68–87 ms across two alternating A/Bs. With both fixes, every game was accepted up to 160 a second (p99 172–204 ms). From 200 a second the single test machine runs out of local ports, not CPU ([evidence](evidence/load_saturation_2026-10-22.json)).
- **Android emulator.** A debug build of the Flutter host played one tapped game on an Android 14 emulator. The score reached the database and the live board, the host received the page's bridge messages, and the app's board updated by itself when other players scored ([evidence](evidence/android_emulator_2026-10-22.json)).
- **Physical phone.** On a Samsung Galaxy S23 (Android 16) connected by USB, a person played both games in the Flutter host (130 and 15 points). Both were accepted in the database, and the phone's leaderboard updated live when another player scored ([evidence](evidence/phone_session_2026-10-22.json)). It was a debug build reaching the Mac through `adb reverse`, not a mobile network or a release build.
- **HTTPS release build, 24 October.** The gateway can now serve TLS itself (`MINIPLAY_TLS_CERT` and `MINIPLAY_TLS_KEY`, TLS 1.2 or later). Each Android build type has its own network policy: debug allows plain HTTP, while release and profile refuse it and trust only system CAs. A non-production `localRelease` build, signed like a release and not debuggable, also trusts one local CA for the emulator's host addresses. On an Android 14 emulator it loaded the game over HTTPS, a tapped game scored 90 and reached SQL and the live board, and two more players scored from the Mac over HTTPS appeared on the app's board by themselves over wss. The production release APK, pointed at the same gateway, refused the local certificate ("certificate authority is not trusted") and nothing reached SQL ([evidence](evidence/android_release_https_2026-10-24.json)). Release builds with no upload key now fail instead of falling back to the debug key. This run also found that, until 23 October, the main manifest allowed plain HTTP in every build; earlier statements that only debug builds allowed it were wrong, and the older evidence files carry a dated correction.
- The offline synthetic abuse evaluation found 22 injected bots, falsely flagged 1 clean run, and missed 58 bots: precision 0.9565 and recall 0.275 on that designed holdout. It is not a real-player accuracy estimate.

[RESULTS.md](RESULTS.md) gives the dates, commands and exact boundaries for these checks.

## Run locally

You need Go (version in go.mod), Node.js, Flutter SDK; PostgreSQL, Kafka, and Redis for the full service path.

```sh
go test ./...
cd web
npm ci
npm test
npm run build
```

The [OpenAPI contract](api/openapi.yaml) describes the gateway. The Flutter app can be checked with `cd flutter_host && flutter pub get && flutter analyze && flutter test`. The built `web/dist` is served by the Go gateway when running locally. `go run ./cmd/e2e-probe -h` and `go run ./cmd/load-probe -h` show the end-to-end probe and the timed load profile; the probe's `-ca` flag trusts a local CA for an HTTPS gateway. [The Flutter host notes](flutter_host/README.md) cover the Android network policy, release signing and the `localRelease` build. Keep certificates' private keys and signing keys outside the repository. Session and score rate limits read the player's address from `X-Forwarded-For` only when the request comes from a trusted proxy (loopback by default, or the CIDRs in `MINIPLAY_TRUSTED_PROXIES`).

## Repository map

`cmd/` and `internal/` contain Go services and replay logic; `web/` contains the games; [flutter_host/](flutter_host/README.md) embeds the client; `db/migrations/` defines durable state; `api/` defines the gateway contract. The old `server.js` and `public/` are a separate Node prototype with older routes.

## Scope

User IDs are demo identities. Next: play the production release build over a real network against a gateway with a publicly trusted certificate, and run the load profile from more than one machine. Authentication, deployed TLS with managed keys, and real-player abuse labels are still open. Since 24 October plain HTTP works only in debug builds of the Flutter host. This is a local prototype, not a public gaming service.
