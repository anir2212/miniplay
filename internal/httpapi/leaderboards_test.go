package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"miniplay/internal/game"
	"miniplay/internal/store"
)

type fallbackBoardStore struct {
	rows        []store.Score
	secondError bool
}

func (*fallbackBoardStore) BestScores(context.Context, game.ID, time.Time) ([]store.Score, error) {
	return nil, errors.New("backfill query failed")
}

func (s *fallbackBoardStore) Leaderboard(context.Context, game.ID, time.Time, string, int) ([]store.Score, error) {
	if s.secondError {
		return nil, errors.New("SQL unavailable")
	}
	return s.rows, nil
}

type partialBoardCache struct{ reads int }

func (*partialBoardCache) HasBoard(context.Context, string) (bool, error) { return false, nil }
func (*partialBoardCache) Backfill(context.Context, string, []store.Score) error {
	return nil
}
func (c *partialBoardCache) Board(context.Context, string, string) (store.Board, error) {
	c.reads++
	return store.Board{Leaders: []store.Leader{{User: "partial", Score: 1}}}, nil
}

func TestLeaderboardBackfillFailureDoesNotServePartialCache(t *testing.T) {
	now := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name       string
		sqlFails   bool
		wantStatus int
	}{
		{name: "SQL fallback succeeds", wantStatus: http.StatusOK},
		{name: "SQL fallback also fails", sqlFails: true, wantStatus: http.StatusServiceUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := &fallbackBoardStore{rows: []store.Score{{User: "complete", Game: game.Rhythm, Score: 20}}, secondError: tc.sqlFails}
			cache := &partialBoardCache{}
			request := httptest.NewRequest(http.MethodGet, "/api/leaderboards?game=rhythm&period=all&user=complete", nil)
			response := httptest.NewRecorder()
			LeaderboardHandler(db, cache, func() time.Time { return now }).ServeHTTP(response, request)
			if response.Code != tc.wantStatus || cache.reads != 0 {
				t.Fatalf("status=%d cache reads=%d body=%s", response.Code, cache.reads, response.Body.String())
			}
			if !tc.sqlFails {
				var board store.Board
				if err := json.Unmarshal(response.Body.Bytes(), &board); err != nil {
					t.Fatal(err)
				}
				if len(board.Leaders) != 1 || board.Leaders[0].User != "complete" || board.MyRank == nil || *board.MyRank != 1 {
					t.Fatalf("fallback board=%+v", board)
				}
			}
		})
	}
}
