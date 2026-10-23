package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"miniplay/internal/game"
)

type Redis struct{ Client *redis.Client }

// A score event can create a sorted set containing just one user. Only a SQL
// backfill may mark a board complete; key existence alone is not enough.
func boardMarker(key string) string { return "complete:" + key }

const updateBoardScript = `
if redis.call('EXISTS', KEYS[1]) == 0 then
  redis.call('DEL', KEYS[2])
end
local changed = redis.call('ZADD', KEYS[1], 'GT', 'CH', ARGV[1], ARGV[2])
if tonumber(ARGV[3]) > 0 then
  redis.call('EXPIRE', KEYS[1], ARGV[3])
end
if changed > 0 then
  redis.call('PUBLISH', KEYS[3], ARGV[4])
end
return changed
`

const backfillBoardScript = `
for i = 2, #ARGV, 2 do
  redis.call('ZADD', KEYS[1], 'GT', ARGV[i], ARGV[i + 1])
end
if redis.call('ZCARD', KEYS[1]) > 0 then
  redis.call('SET', KEYS[2], 'ready')
  if tonumber(ARGV[1]) > 0 then
    redis.call('EXPIRE', KEYS[2], ARGV[1])
    redis.call('EXPIRE', KEYS[1], ARGV[1])
  end
else
  -- Retry empty SQL boards periodically in case a score update was missed.
  redis.call('SET', KEYS[2], 'empty', 'EX', 30)
end
return 1
`

const boardReadyScript = `
local state = redis.call('GET', KEYS[2])
local exists = redis.call('EXISTS', KEYS[1])
if (state == 'ready' and exists == 1) or (state == 'empty' and exists == 0) then
  return 1
end
return 0
`

func boardTTL(key string) int {
	if len(key) >= 4 && key[len(key)-4:] == ":all" {
		return 0
	}
	return int((9 * 24 * time.Hour).Seconds())
}

func OpenRedis(ctx context.Context, url string) (*Redis, error) {
	opts, err := redis.ParseURL(url)
	if err != nil {
		return nil, err
	}
	client := redis.NewClient(opts)
	if err = client.Ping(ctx).Err(); err != nil {
		client.Close()
		return nil, err
	}
	return &Redis{Client: client}, nil
}

type Period string

const (
	All    Period = "all"
	Daily  Period = "daily"
	Weekly Period = "weekly"
)

func BoardKey(id game.ID, period Period, at time.Time) (string, time.Time, error) {
	if !game.ValidID(id) {
		return "", time.Time{}, fmt.Errorf("unknown game")
	}
	at = at.UTC()
	switch period {
	case All:
		return "board:" + string(id) + ":all", time.Time{}, nil
	case Daily:
		day := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
		return "board:" + string(id) + ":day:" + day.Format("2006-01-02"), day, nil
	case Weekly:
		year, week := at.ISOWeek()
		daysSinceMonday := (int(at.Weekday()) + 6) % 7
		monday := time.Date(at.Year(), at.Month(), at.Day()-daysSinceMonday, 0, 0, 0, 0, time.UTC)
		return fmt.Sprintf("board:%s:week:%04d-%02d", id, year, week), monday, nil
	default:
		return "", time.Time{}, fmt.Errorf("unknown period")
	}
}

func (r *Redis) UpdateBoards(ctx context.Context, row Score) error {
	for _, period := range []Period{All, Daily, Weekly} {
		key, _, err := BoardKey(row.Game, period, row.SubmittedAt)
		if err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{"user": row.User, "score": row.Score, "game": row.Game, "period": period})
		// Key creation, marker invalidation, ZADD GT, and publication are
		// atomic. A retry cannot lower a user's best score. Only a change is
		// published: the stats worker re-applies every accepted score, and each
		// notification makes every live socket on the board rebuild a frame.
		if err = r.Client.Eval(ctx, updateBoardScript, []string{key, boardMarker(key), "updates:" + key}, row.Score, row.User, boardTTL(key), string(payload)).Err(); err != nil {
			return err
		}
	}
	return nil
}

func (r *Redis) HasBoard(ctx context.Context, key string) (bool, error) {
	ready, err := r.Client.Eval(ctx, boardReadyScript, []string{key, boardMarker(key)}).Int()
	return ready == 1, err
}

func (r *Redis) Backfill(ctx context.Context, key string, rows []Score) error {
	args := make([]any, 0, 1+len(rows)*2)
	args = append(args, boardTTL(key))
	for _, row := range rows {
		args = append(args, row.Score, row.User)
	}
	return r.Client.Eval(ctx, backfillBoardScript, []string{key, boardMarker(key)}, args...).Err()
}

type Leader struct {
	User  string `json:"user"`
	Score int    `json:"score"`
}
type Board struct {
	Leaders []Leader `json:"leaders"`
	MyRank  *int64   `json:"myRank"`
}

func (r *Redis) Board(ctx context.Context, key, user string) (Board, error) {
	top, err := r.Client.ZRevRangeWithScores(ctx, key, 0, 19).Result()
	if err != nil {
		return Board{}, err
	}
	result := Board{Leaders: make([]Leader, 0, len(top))}
	for _, member := range top {
		result.Leaders = append(result.Leaders, Leader{User: fmt.Sprint(member.Member), Score: int(member.Score)})
	}
	if user != "" {
		rank, err := r.Client.ZRevRank(ctx, key, user).Result()
		if err != nil && err != redis.Nil {
			return Board{}, err
		}
		if err == nil {
			oneBased := rank + 1
			result.MyRank = &oneBased
		}
	}
	return result, nil
}

// SubscribeBoard coalesces bursts of score notifications. The caller reads a
// fresh board after each notification, so dropped intermediate notices do not
// lose the latest rank state.
func (r *Redis) SubscribeBoard(ctx context.Context, key string) (<-chan struct{}, func(), error) {
	subCtx, cancel := context.WithCancel(ctx)
	pubsub := r.Client.Subscribe(subCtx, "updates:"+key)
	if _, err := pubsub.Receive(subCtx); err != nil {
		cancel()
		_ = pubsub.Close()
		return nil, nil, err
	}
	updates := make(chan struct{}, 1)
	raw := pubsub.Channel()
	go func() {
		defer close(updates)
		for {
			select {
			case <-subCtx.Done():
				return
			case _, ok := <-raw:
				if !ok {
					return
				}
				select {
				case updates <- struct{}{}:
				default:
				}
			}
		}
	}()
	return updates, func() { cancel(); _ = pubsub.Close() }, nil
}

const tokenBucket = `
local data = redis.call('HMGET', KEYS[1], 'tokens', 'last')
local tokens = tonumber(data[1]) or tonumber(ARGV[1])
local last = tonumber(data[2]) or tonumber(ARGV[3])
local now = tonumber(ARGV[3])
tokens = math.min(tonumber(ARGV[1]), tokens + math.max(0, now-last) * tonumber(ARGV[2]))
if tokens < 1 then
  redis.call('HMSET', KEYS[1], 'tokens', tokens, 'last', now)
  redis.call('EXPIRE', KEYS[1], tonumber(ARGV[4]))
  return 0
end
redis.call('HMSET', KEYS[1], 'tokens', tokens-1, 'last', now)
redis.call('EXPIRE', KEYS[1], tonumber(ARGV[4]))
return 1
`

func (r *Redis) Allow(ctx context.Context, key string, capacity int, refillPerSecond float64) (bool, error) {
	allowed, err := r.Client.Eval(ctx, tokenBucket, []string{"rate:" + key}, capacity, refillPerSecond, float64(time.Now().UnixMilli())/1000, 120).Int()
	return allowed == 1, err
}

// Ranks returns one-based ranks for the users on the board in one pipelined
// round trip. The live broadcaster uses it to give every watching player
// their own rank after a change without one request per socket.
func (r *Redis) Ranks(ctx context.Context, key string, users []string) (map[string]int64, error) {
	ranks := make(map[string]int64, len(users))
	if len(users) == 0 {
		return ranks, nil
	}
	pipe := r.Client.Pipeline()
	commands := make([]*redis.IntCmd, len(users))
	for i, user := range users {
		commands[i] = pipe.ZRevRank(ctx, key, user)
	}
	if _, err := pipe.Exec(ctx); err != nil && err != redis.Nil {
		return nil, err
	}
	for i, command := range commands {
		rank, err := command.Result()
		if err == redis.Nil {
			continue
		}
		if err != nil {
			return nil, err
		}
		ranks[users[i]] = rank + 1
	}
	return ranks, nil
}
