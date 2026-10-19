# MiniPlay

Small games are easy to put on a page; trustworthy scores and live leaderboards are harder. MiniPlay explores that problem with two original ten-second Phaser games, **Pulse Field** and **Comet Catch**. A Go service signs each run and replays submitted taps before accepting a score. PostgreSQL keeps the durable record, Redis serves rankings, Kafka carries score events, and a Flutter host embeds the web client.

The server can reject a score that does not match the submitted taps. It cannot prove a human played: a bot may fabricate plausible input. The abuse scanner therefore produces review candidates rather than automatic bans.

## What has been checked

- The web game rules, build, and desktop Chrome game-start smoke passed; Flutter analysis and bridge parser tests passed.
- In an isolated PostgreSQL test, 12 concurrent submissions of one run ID produced 1 accepted write and 11 duplicates. The records survived a clean database restart.
- An isolated Redis 7 test exercised partial-board detection, SQL backfill, score retry, eviction recovery, and empty-board handling. Gateway → session → score was also checked over loopback with fake storage. These are separate tests; the complete PostgreSQL/Redis/Kafka path remains unverified.
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

The [OpenAPI contract](api/openapi.yaml) describes the gateway. The Flutter app can be checked with `cd flutter_host && flutter pub get && flutter analyze && flutter test`. The built `web/dist` is served by the Go gateway when running locally.

## Repository map

`cmd/` and `internal/` contain Go services and replay logic; `web/` contains the games; [flutter_host/](flutter_host/README.md) embeds the client; `db/migrations/` defines durable state; `api/` defines the gateway contract. The old `server.js` and `public/` are a separate Node prototype with older routes.

## Scope

User IDs are demo identities. The next runtime milestone is one browser score traversing Go, PostgreSQL, Kafka, the worker, Redis and a live board, including restart and duplicate checks. Android emulator play, authentication, TLS, real-player abuse labels, and documented latency tests are still open. This is a local prototype, not a public gaming service.
