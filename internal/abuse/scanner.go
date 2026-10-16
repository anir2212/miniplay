package abuse

import (
	"context"
	"fmt"
	"time"

	"miniplay/internal/game"
	"miniplay/internal/store"
)

type Repository interface {
	RecentRuns(context.Context, time.Time) ([]store.ScoredRun, error)
	SaveFlag(context.Context, store.FlagRecord) error
}
type ScanReport struct {
	Scanned      int       `json:"scanned"`
	Flagged      int       `json:"flagged"`
	SkippedGames []game.ID `json:"skippedGames"`
}

func Scan(ctx context.Context, repo Repository, now time.Time) (ScanReport, error) {
	runs, err := repo.RecentRuns(ctx, now.Add(-30*24*time.Hour))
	if err != nil {
		return ScanReport{}, err
	}
	report := ScanReport{Scanned: len(runs), SkippedGames: []game.ID{}}
	for _, id := range []game.ID{game.Rhythm, game.Reflex} {
		selected := make([]store.ScoredRun, 0)
		features := make([]Vector, 0)
		scores := make([]float64, 0)
		for _, run := range runs {
			if run.Game == id {
				vector, err := Features(id, run.RunID, run.Score.Score, run.DurationMS, run.Events)
				if err != nil {
					return report, err
				}
				selected = append(selected, run)
				features = append(features, vector)
				scores = append(scores, float64(run.Score.Score))
			}
		}
		if len(selected) < 30 {
			report.SkippedGames = append(report.SkippedGames, id)
			continue
		}
		forest, err := Fit(features, 100, 64, 47211)
		if err != nil {
			return report, err
		}
		isolation := make([]float64, len(features))
		for index, vector := range features {
			isolation[index] = forest.Score(vector)
		}
		threshold := Quantile(isolation, 0.98)
		for index, run := range selected {
			z := ZScore(float64(run.Score.Score), scores)
			reasons := Reasons(isolation[index], threshold, z)
			if len(reasons) == 0 {
				continue
			}
			vector := features[index]
			flag := store.FlagRecord{RunID: run.RunID, User: run.User, Game: id, IsolationScore: isolation[index], ZScore: z, Reasons: reasons,
				Evidence: map[string]float64{"scoreFraction": vector[0], "durationSeconds": vector[1], "meanTimingErrorMs": vector[2], "cadenceCV": vector[3], "tapCount": vector[4]}}
			if err := repo.SaveFlag(ctx, flag); err != nil {
				return report, fmt.Errorf("save flag %s: %w", run.RunID, err)
			}
			report.Flagged++
		}
	}
	return report, nil
}

func Reasons(isolation, threshold, z float64) []string {
	reasons := make([]string, 0, 2)
	if isolation >= threshold && isolation >= 0.55 {
		reasons = append(reasons, "isolation-forest outlier")
	}
	if z >= 3 {
		reasons = append(reasons, "high-score z-score")
	}
	return reasons
}
