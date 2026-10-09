package module

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

// Redis keys shared with the backend (backend/internal/repo/persistent/view).
const (
	viewCounterPrefix = "video_views:"
	// inflightKey holds the batch currently being written to Postgres:
	// videoID -> claimed view count.
	inflightKey = "view_sync:inflight"
	// inflightBatchKey holds that batch's ID (the idempotency key).
	inflightBatchKey = "view_sync:inflight_batch"
)

// claimScript atomically moves a counter's whole value into the in-flight
// hash, so views that arrive while a batch is being written are neither lost
// nor double-counted — they simply stay in the counter for the next run.
var claimScript = redis.NewScript(`
local v = tonumber(redis.call('GET', KEYS[1]) or '0')
if v <= 0 then return 0 end
local left = redis.call('DECRBY', KEYS[1], v)
if left == 0 then redis.call('DEL', KEYS[1]) end
redis.call('HINCRBY', KEYS[2], ARGV[1], v)
return v
`)

// ViewSync flushes the Redis view counters into videos.views every run
// (scheduled @every 2m, per BUSINESS_REQUIREMENTS.md §3.1).
//
// Exactly-once, even across crashes:
//  1. claim: counters are moved into an in-flight hash tagged with a batch ID
//  2. apply: one Postgres transaction inserts the batch ID into
//     view_sync_batches and adds every count — a batch ID that is already
//     there means a previous run committed it and only crashed afterwards
//  3. release: the in-flight hash is deleted only after the commit
//
// A leftover in-flight batch from a crashed run is always finished first,
// before anything new is claimed.
type ViewSync struct {
	Redis  *redis.Client
	Logger *zap.Logger
}

func (v *ViewSync) Run() {
	if v.Redis == nil {
		return
	}

	pgURL := os.Getenv("PG_URL")
	if pgURL == "" {
		v.Logger.Error("ViewSync: PG_URL is not set, skipping run")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Finish a batch left behind by a crashed run before claiming new views.
	if err := v.flushInflight(ctx, pgURL); err != nil {
		v.Logger.Error("ViewSync: failed to flush leftover in-flight batch", zap.Error(err))
		return
	}

	claimed, err := v.claim(ctx)
	if err != nil {
		v.Logger.Error("ViewSync: failed to claim Redis counters", zap.Error(err))
		return
	}
	if claimed == 0 {
		return
	}

	if err := v.flushInflight(ctx, pgURL); err != nil {
		// The claimed views stay in the in-flight hash and are retried next run.
		v.Logger.Error("ViewSync: failed to apply batch, will retry next run", zap.Error(err))
	}
}

// claim starts a new batch and moves every pending counter into it.
func (v *ViewSync) claim(ctx context.Context) (int, error) {
	if err := v.Redis.Set(ctx, inflightBatchKey, uuid.NewString(), 0).Err(); err != nil {
		return 0, fmt.Errorf("set batch id: %w", err)
	}

	claimed := 0
	var cursor uint64
	for {
		keys, next, err := v.Redis.Scan(ctx, cursor, viewCounterPrefix+"*", 100).Result()
		if err != nil {
			return claimed, fmt.Errorf("scan: %w", err)
		}
		for _, key := range keys {
			videoID := strings.TrimPrefix(key, viewCounterPrefix)
			n, err := claimScript.Run(ctx, v.Redis, []string{key, inflightKey}, videoID).Int64()
			if err != nil {
				return claimed, fmt.Errorf("claim %s: %w", key, err)
			}
			if n > 0 {
				claimed++
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}

	if claimed == 0 {
		v.Redis.Del(ctx, inflightBatchKey)
	}
	return claimed, nil
}

// flushInflight applies the in-flight batch (if any) to Postgres exactly once
// and then releases it from Redis.
func (v *ViewSync) flushInflight(ctx context.Context, pgURL string) error {
	batchID, err := v.Redis.Get(ctx, inflightBatchKey).Result()
	if errors.Is(err, redis.Nil) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get batch id: %w", err)
	}

	counts, err := v.Redis.HGetAll(ctx, inflightKey).Result()
	if err != nil {
		return fmt.Errorf("read in-flight batch: %w", err)
	}

	ids := make([]string, 0, len(counts))
	incs := make([]int64, 0, len(counts))
	var total int64
	for id, s := range counts {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n <= 0 {
			continue
		}
		ids = append(ids, id)
		incs = append(incs, n)
		total += n
	}

	if len(ids) > 0 {
		applied, err := applyBatch(ctx, pgURL, batchID, ids, incs, total)
		if err != nil {
			return err
		}
		if applied {
			v.Logger.Info("ViewSync: synced views to PostgreSQL",
				zap.String("batch_id", batchID), zap.Int("videos", len(ids)), zap.Int64("views", total))
		} else {
			v.Logger.Warn("ViewSync: batch was already applied before a crash, skipping", zap.String("batch_id", batchID))
		}
	}

	return v.Redis.Del(ctx, inflightKey, inflightBatchKey).Err()
}

// applyBatch returns false (and changes nothing) if batchID was already applied.
func applyBatch(ctx context.Context, pgURL, batchID string, ids []string, incs []int64, total int64) (bool, error) {
	conn, err := pgx.Connect(ctx, pgURL)
	if err != nil {
		return false, fmt.Errorf("connect postgres: %w", err)
	}
	defer conn.Close(ctx)

	tx, err := conn.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback(ctx)

	tag, err := tx.Exec(ctx,
		`INSERT INTO view_sync_batches (batch_id, video_count, view_count) VALUES ($1, $2, $3) ON CONFLICT (batch_id) DO NOTHING`,
		batchID, len(ids), total)
	if err != nil {
		return false, fmt.Errorf("record batch: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}

	// One statement for the whole batch: an unknown video ID simply matches
	// no row instead of aborting the transaction.
	if _, err := tx.Exec(ctx,
		`UPDATE videos AS v SET views = v.views + d.inc
		 FROM unnest($1::text[], $2::bigint[]) AS d(id, inc)
		 WHERE v.id = d.id`,
		ids, incs); err != nil {
		return false, fmt.Errorf("update views: %w", err)
	}

	// The idempotency log only needs to outlive a crash-and-retry window.
	if _, err := tx.Exec(ctx, `DELETE FROM view_sync_batches WHERE applied_at < NOW() - INTERVAL '7 days'`); err != nil {
		return false, fmt.Errorf("prune batches: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit: %w", err)
	}
	return true, nil
}
