package store

import "sort"

func sortScores(rows []Score) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Score != rows[j].Score {
			return rows[i].Score > rows[j].Score
		}
		// Redis ZREVRANGE orders equal scores by member in reverse lexical order.
		return rows[i].User > rows[j].User
	})
}
