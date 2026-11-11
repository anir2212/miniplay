# Database-backed abuse-scan check — frozen protocol

Frozen on 11 November 2026 before implementing the fixture generator, inserting rows, or running the scored scan. This is a local integration and limitation check, not an estimate of real-player cheat detection.

## Scope and fixed fixture

Use a fresh disposable PostgreSQL database named exactly `miniplay_abuse_eval`, with migrations 001–003 applied and no pre-existing scored runs. Do not connect to the everyday database. Generate 100 synthetic runs for each game (`rhythm` and `reflex`), with IDs `abuse-db-<game>-000` through `abuse-db-<game>-099` and distinct users of the same suffix. For each game:

| Indices | Label | Taps |
|---|---|---|
| 000–059 | clean-labelled synthetic control | First `3 + (index % 4)` game targets. Each tap uses its target's coordinates and timestamp plus `((index * 17 + target_index * 13) % 121) - 60` milliseconds. |
| 060–079 | injected, overt | Every target, at its exact coordinates and timestamp. |
| 080–099 | injected, mimic | The same tap rule as the clean-labelled controls, using this run's own index and targets. This intentionally makes a class the current features may not distinguish. |

Use the current `game.Targets` and `game.Replay` for every fixture's target stream and base score. All runs have a 10,000 ms duration. Every fifth index (`index % 5 == 0`) has multiplier 2 and flag version 1; all others have multiplier 1 and flag version 0. Store `score = base_score * multiplier` and the taps through `store.RecordAttempt`, which writes `run_attempts`, `scores` and `event_outbox` in one transaction. This bypasses signed HTTP session validation; the generated taps and scores still pass the replay oracle. No label or fixture class goes into the database rows read by the scanner.

## Predeclared run and acceptance

Run the unchanged production `abuse.Scan` against the disposable database, then read its persisted `flagged_runs` through `store.Flags`. The scanner fits its forest on the same recent rows it scans, with its existing seed and thresholds; this is an in-sample check of the storage and scan path. Do not change the scanner, fixture recipe or thresholds after seeing results. Repeat the scan once to check that upserting flags does not duplicate rows.

Required validity checks: exactly 200 inserted attempts, scores and outbox rows; exactly 100 rows per game; every stored score equals its replayed base score times its multiplier; 200 rows reported scanned; `ScanReport.Flagged` equals the persisted flag count; every flagged ID belongs to the fixture; and the second scan leaves the flag count unchanged. Abort and record the failure if any validity check fails.

Report a confusion matrix for all injected runs combined and separately for overt and mimic injections, per-game counts, precision and recall where defined, and the 20% double-points subset's flag counts. These are descriptive results with no pass threshold. Save one JSON result at `evidence/abuse_db_2026-11-11.json`, including the exact protocol SHA-256 and environment versions, without database credentials. A second execution on a new disposable database should match the label/flag rows and aggregate measurements; timestamps may differ. Record any failure or difference instead of rewriting the first result.

## Limits

Every class is author-generated, including the clean-labelled controls. The mimic class is deliberately feature-similar to controls; its labels represent the generator's intent, not an observed person or bot. A database insert through `RecordAttempt` proves durable storage and scanner behavior, not live gateway authentication, signed run tokens, Redis/Kafka, a real game session, or production detection quality. Keep the earlier feature-vector result dated and separate.
