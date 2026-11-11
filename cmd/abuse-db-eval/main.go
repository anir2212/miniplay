// abuse-db-eval exercises the production abuse scanner against a throwaway
// PostgreSQL database. Its synthetic labels are not real-player evidence.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"runtime"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"miniplay/internal/abuse"
	"miniplay/internal/game"
	"miniplay/internal/migrate"
	"miniplay/internal/store"
)

const databaseName = "miniplay_abuse_eval"

type fixtureLabel struct {
	game    game.ID
	class   string
	doubled bool
}

type groupResult struct {
	Clean        int `json:"clean"`
	CleanFlagged int `json:"cleanFlagged"`
	Overt        int `json:"overt"`
	OvertFlagged int `json:"overtFlagged"`
	Mimic        int `json:"mimic"`
	MimicFlagged int `json:"mimicFlagged"`
}

type report struct {
	ProtocolSHA256      string                 `json:"protocolSha256"`
	GoVersion           string                 `json:"goVersion"`
	PostgresVersion     string                 `json:"postgresVersion"`
	FixtureCounts       map[string]int         `json:"fixtureCounts"`
	Scanned             int                    `json:"scanned"`
	Flagged             int                    `json:"flagged"`
	FlaggedOnRepeat     int                    `json:"flaggedOnRepeat"`
	FlaggedRunIDs       []string               `json:"flaggedRunIds"`
	Groups              map[string]groupResult `json:"groups"`
	TruePositive        int                    `json:"truePositive"`
	FalsePositive       int                    `json:"falsePositive"`
	FalseNegative       int                    `json:"falseNegative"`
	Precision           float64                `json:"precision"`
	Recall              float64                `json:"recall"`
	OvertRecall         float64                `json:"overtRecall"`
	MimicRecall         float64                `json:"mimicRecall"`
	DoublePointsFlagged int                    `json:"doublePointsFlagged"`
	DoublePointsTotal   int                    `json:"doublePointsTotal"`
}

func fixture(id game.ID, index int) (store.Score, []game.Tap, fixtureLabel, error) {
	runID := fmt.Sprintf("abuse-db-%s-%03d", id, index)
	targets, err := game.Targets(id, runID)
	if err != nil {
		return store.Score{}, nil, fixtureLabel{}, err
	}
	class := "clean"
	count := 3 + index%4
	if index >= 60 && index < 80 {
		class, count = "overt", len(targets)
	} else if index >= 80 {
		class = "mimic"
	}
	taps := make([]game.Tap, 0, count)
	for targetIndex, target := range targets[:count] {
		ms := target.MS
		if class != "overt" {
			ms += ((index*17 + targetIndex*13) % 121) - 60
		}
		taps = append(taps, game.Tap{MS: ms, X: target.X, Y: target.Y})
	}
	baseScore, err := game.Replay(id, runID, taps)
	if err != nil {
		return store.Score{}, nil, fixtureLabel{}, fmt.Errorf("replay %s: %w", runID, err)
	}
	multiplier, flagVersion := 1, int64(0)
	if index%5 == 0 {
		multiplier, flagVersion = 2, 1
	}
	row := store.Score{
		RunID: runID, User: runID, Game: id,
		Score: baseScore * multiplier, BaseScore: baseScore,
		Multiplier: multiplier, FlagVersion: flagVersion,
		DurationMS: game.RunMilliseconds,
	}
	return row, taps, fixtureLabel{game: id, class: class, doubled: multiplier == 2}, nil
}

func evaluate(ctx context.Context, url string) (report, error) {
	protocol, err := os.ReadFile("docs/ABUSE_DB_PROTOCOL.md")
	if err != nil {
		return report{}, err
	}
	digest := sha256.Sum256(protocol)
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		return report{}, err
	}
	defer conn.Close(ctx)
	var name, version string
	var existing *string
	if err := conn.QueryRow(ctx, `SELECT current_database(),version(),to_regclass('public.schema_migrations')::text`).Scan(&name, &version, &existing); err != nil {
		return report{}, err
	}
	if name != databaseName || existing != nil {
		return report{}, fmt.Errorf("refusing database %q: use a fresh disposable %s", name, databaseName)
	}
	applied, err := migrate.Apply(ctx, conn, "db/migrations")
	if err != nil {
		return report{}, err
	}
	if applied != 3 {
		return report{}, fmt.Errorf("expected three migrations on a fresh database, applied %d", applied)
	}
	db, err := store.Open(ctx, url)
	if err != nil {
		return report{}, err
	}
	defer db.Pool.Close()
	labels := make(map[string]fixtureLabel, 200)
	for _, id := range []game.ID{game.Rhythm, game.Reflex} {
		for index := 0; index < 100; index++ {
			row, taps, label, err := fixture(id, index)
			if err != nil {
				return report{}, err
			}
			if _, err := db.RecordAttempt(ctx, row, taps, ""); err != nil {
				return report{}, fmt.Errorf("insert %s: %w", row.RunID, err)
			}
			labels[row.RunID] = label
		}
	}
	var scores, attempts, outbox, rhythm, reflex, explainable int
	if err := db.Pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM scores),
		(SELECT count(*) FROM run_attempts),
		(SELECT count(*) FROM event_outbox),
		(SELECT count(*) FROM scores WHERE game='rhythm'),
		(SELECT count(*) FROM scores WHERE game='reflex'),
		(SELECT count(*) FROM scores WHERE score=base_score*multiplier)`).Scan(
		&scores, &attempts, &outbox, &rhythm, &reflex, &explainable); err != nil {
		return report{}, err
	}
	if scores != 200 || attempts != 200 || outbox != 200 || rhythm != 100 || reflex != 100 || explainable != 200 {
		return report{}, fmt.Errorf("invalid row counts: scores=%d attempts=%d outbox=%d rhythm=%d reflex=%d explainable=%d",
			scores, attempts, outbox, rhythm, reflex, explainable)
	}
	now := time.Now().UTC()
	stored, err := db.RecentRuns(ctx, now.Add(-30*24*time.Hour))
	if err != nil {
		return report{}, err
	}
	if len(stored) != 200 {
		return report{}, fmt.Errorf("RecentRuns returned %d rows, want 200", len(stored))
	}
	for _, run := range stored {
		base, err := game.Replay(run.Game, run.RunID, run.Events)
		if err != nil || base != run.BaseScore || run.Score.Score != base*run.Multiplier {
			return report{}, fmt.Errorf("stored replay mismatch for %s: base=%d err=%v", run.RunID, base, err)
		}
	}
	first, err := abuse.Scan(ctx, db, now)
	if err != nil {
		return report{}, err
	}
	flags, err := db.Flags(ctx, 500)
	if err != nil {
		return report{}, err
	}
	if first.Scanned != 200 || first.Flagged != len(flags) || len(first.SkippedGames) != 0 {
		return report{}, fmt.Errorf("scan/persistence mismatch: scan=%+v flags=%d", first, len(flags))
	}
	second, err := abuse.Scan(ctx, db, now)
	if err != nil {
		return report{}, err
	}
	flagsAgain, err := db.Flags(ctx, 500)
	if err != nil {
		return report{}, err
	}
	if second.Scanned != 200 || second.Flagged != first.Flagged || len(flagsAgain) != len(flags) {
		return report{}, fmt.Errorf("repeat scan changed counts: first=%+v second=%+v flags=%d/%d", first, second, len(flags), len(flagsAgain))
	}
	firstIDs := make([]string, 0, len(flags))
	secondIDs := make([]string, 0, len(flagsAgain))
	for _, flag := range flags {
		firstIDs = append(firstIDs, flag.RunID)
	}
	for _, flag := range flagsAgain {
		secondIDs = append(secondIDs, flag.RunID)
	}
	sort.Strings(firstIDs)
	sort.Strings(secondIDs)
	for index := range firstIDs {
		if firstIDs[index] != secondIDs[index] {
			return report{}, fmt.Errorf("repeat scan changed flagged run IDs")
		}
	}
	result := report{
		ProtocolSHA256: fmt.Sprintf("%x", digest), GoVersion: runtime.Version(), PostgresVersion: version,
		FixtureCounts: map[string]int{"scores": scores, "attempts": attempts, "outbox": outbox, "rhythm": rhythm, "reflex": reflex, "explainable": explainable},
		Scanned:       first.Scanned, Flagged: first.Flagged, FlaggedOnRepeat: len(flagsAgain), FlaggedRunIDs: firstIDs,
		Groups: map[string]groupResult{"rhythm": {}, "reflex": {}}, DoublePointsTotal: 40,
	}
	for _, id := range []game.ID{game.Rhythm, game.Reflex} {
		group := result.Groups[string(id)]
		for index := 0; index < 100; index++ {
			label := labels[fmt.Sprintf("abuse-db-%s-%03d", id, index)]
			switch label.class {
			case "clean":
				group.Clean++
			case "overt":
				group.Overt++
			case "mimic":
				group.Mimic++
			}
		}
		result.Groups[string(id)] = group
	}
	for _, flag := range flags {
		label, ok := labels[flag.RunID]
		if !ok || flag.Game != label.game {
			return report{}, fmt.Errorf("unexpected flagged run %s", flag.RunID)
		}
		group := result.Groups[string(label.game)]
		switch label.class {
		case "clean":
			group.CleanFlagged++
			result.FalsePositive++
		case "overt":
			group.OvertFlagged++
			result.TruePositive++
		case "mimic":
			group.MimicFlagged++
			result.TruePositive++
		}
		if label.doubled {
			result.DoublePointsFlagged++
		}
		result.Groups[string(label.game)] = group
	}
	result.FalseNegative = 80 - result.TruePositive
	if result.TruePositive+result.FalsePositive > 0 {
		result.Precision = float64(result.TruePositive) / float64(result.TruePositive+result.FalsePositive)
	}
	result.Recall = float64(result.TruePositive) / 80
	overt, mimic := 0, 0
	for _, group := range result.Groups {
		overt += group.OvertFlagged
		mimic += group.MimicFlagged
	}
	result.OvertRecall, result.MimicRecall = float64(overt)/40, float64(mimic)/40
	return result, nil
}

func main() {
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		log.Fatal("DATABASE_URL must point to a fresh disposable miniplay_abuse_eval database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	result, err := evaluate(ctx, url)
	if err != nil {
		log.Fatal(err)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		log.Fatal(err)
	}
}
