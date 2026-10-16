# MiniPlay

Small browser games with replay-checked scores.

Two browser mini-games with a Flutter host and Go services for runs, scoreboards, replay validation, and basic abuse review.

## Why this project

A leaderboard is only useful if the server can question a submitted score. This project pairs small games with replay-checked runs and a backend scoreboard to explore what can be verified from client input—and what still cannot.

## Run locally

You need Go (version in go.mod), Node.js, Flutter SDK; PostgreSQL, Kafka, and Redis for the full service path.

```sh
go test ./...
cd web
npm ci
npm test
npm run build
```

The [OpenAPI contract](api/openapi.yaml) describes the gateway. The Flutter app can be checked with `cd flutter_host && flutter pub get && flutter analyze && flutter test`. [Results](RESULTS.md) lists the verified local service and client checks.

## Repository map

`cmd/` and `internal/` contain Go services; `web/` contains the games; `flutter_host/` embeds the client; `api/` defines the gateway contract.

## Scope

User IDs are demo identities. Replay rejects inconsistent input/score pairs, but plausible fabricated input remains possible. Full Kafka/API integration and Android emulator validation are pending.
