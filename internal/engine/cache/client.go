// Package cache wraps the engine's Redis connection (direct or Sentinel
// failover, selected by whether SentinelAddrs is set), pinging eagerly at
// construction so a Stage 1 connectivity failure surfaces immediately
// rather than on first use.
package cache

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/redis/go-redis/v9"
)

type Client struct {
	rdb *redis.Client
}

type Config struct {
	Addr          string
	MasterName    string
	SentinelAddrs []string
	DB            int
	MaxRetries    int
}

func New(ctx context.Context, cfg Config) (*Client, error) {
	var rdb *redis.Client

	if len(cfg.SentinelAddrs) > 0 {
		rdb = redis.NewFailoverClient(&redis.FailoverOptions{
			MasterName:    cfg.MasterName,
			SentinelAddrs: cfg.SentinelAddrs,
			DB:            cfg.DB,
			MaxRetries:    cfg.MaxRetries,
		})
	} else {
		rdb = redis.NewClient(&redis.Options{
			Addr:       cfg.Addr,
			DB:         cfg.DB,
			MaxRetries: cfg.MaxRetries,
		})
	}

	if err := rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}

	return &Client{rdb: rdb}, nil
}

func (c *Client) Ping(ctx context.Context) error {
	return c.rdb.Ping(ctx).Err()
}

func (c *Client) Close() error {
	return c.rdb.Close()
}

// SetWithTTL sets key to value, expiring after ttl.
func (c *Client) SetWithTTL(ctx context.Context, key, value string, ttl time.Duration) error {
	if err := c.rdb.Set(ctx, key, value, ttl).Err(); err != nil {
		return fmt.Errorf("set %q: %w", key, err)
	}
	return nil
}

// SetNXWithTTL sets key to value, expiring after ttl, only if key isn't
// already set — Redis SETNX. Returns whether this call was the one that
// set it, so callers doing atomic claim-a-slot-once patterns (auth-
// internals.md §7/§8's mfa_token consumption and TOTP replay protection)
// don't need a separate check-then-set that could race.
func (c *Client) SetNXWithTTL(ctx context.Context, key, value string, ttl time.Duration) (set bool, err error) {
	set, err = c.rdb.SetNX(ctx, key, value, ttl).Result()
	if err != nil {
		return false, fmt.Errorf("setnx %q: %w", key, err)
	}
	return set, nil
}

// IncrWithTTL atomically increments key (creating it at 1 if unset) and
// returns the new count. The expiry is set only on the increment that
// creates key (count == 1), so an existing counter's remaining TTL is
// never extended by a later increment — auth-internals.md §8's MFA
// lockout counter needs failures within a single ttl-wide window to
// accumulate toward the threshold, not have that window keep sliding
// forward on every failed attempt.
func (c *Client) IncrWithTTL(ctx context.Context, key string, ttl time.Duration) (count int64, err error) {
	count, err = c.rdb.Incr(ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("incr %q: %w", key, err)
	}
	if count == 1 {
		if err := c.rdb.Expire(ctx, key, ttl).Err(); err != nil {
			return count, fmt.Errorf("expire %q: %w", key, err)
		}
	}
	return count, nil
}

// Exists reports whether key is currently set.
func (c *Client) Exists(ctx context.Context, key string) (bool, error) {
	n, err := c.rdb.Exists(ctx, key).Result()
	if err != nil {
		return false, fmt.Errorf("check %q: %w", key, err)
	}
	return n > 0, nil
}

// Get reads back key's value. found is false, with a nil error, when key
// isn't set — a cache miss isn't itself an error.
func (c *Client) Get(ctx context.Context, key string) (value string, found bool, err error) {
	value, err = c.rdb.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get %q: %w", key, err)
	}
	return value, true, nil
}

// Delete removes key. Not an error if key wasn't set.
func (c *Client) Delete(ctx context.Context, key string) error {
	if err := c.rdb.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("delete %q: %w", key, err)
	}
	return nil
}

// slidingWindowScript implements a genuine sliding-window log (a Redis
// sorted set of per-request timestamps), not a fixed-window counter:
// IncrWithTTL's fixed-window approach lets a burst of 2x limit through
// across a single window-boundary reset (limit requests just before the
// boundary, limit more just after) — the sorted set's own score-based
// eviction has no boundary to burst across. Runs as one EVAL so the
// trim/count/conditional-add sequence is atomic against a concurrent
// request racing the same key; a Go-side read-then-write would have a
// TOCTOU gap two concurrent requests could both slip through.
const slidingWindowScript = `
local key = KEYS[1]
local now_ms = tonumber(ARGV[1])
local window_ms = tonumber(ARGV[2])
local limit = tonumber(ARGV[3])
local member = ARGV[4]

redis.call('ZREMRANGEBYSCORE', key, '-inf', now_ms - window_ms)
local count = redis.call('ZCARD', key)

if count < limit then
    redis.call('ZADD', key, now_ms, member)
    redis.call('PEXPIRE', key, window_ms)
    return {1, 0}
end

local oldest = redis.call('ZRANGE', key, 0, 0, 'WITHSCORES')
local retry_after_ms = window_ms - (now_ms - tonumber(oldest[2]))
if retry_after_ms < 0 then
    retry_after_ms = 0
end
return {0, retry_after_ms}
`

// SlidingWindowAllow reports whether one more request against key is
// allowed within limit requests per window, atomically recording this
// request if so. retryAfter is only meaningful when allowed is false —
// the time until the window's oldest recorded request ages out and a
// slot frees up.
func (c *Client) SlidingWindowAllow(ctx context.Context, key string, limit int, window time.Duration) (allowed bool, retryAfter time.Duration, err error) {
	member := fmt.Sprintf("%d-%d", time.Now().UnixNano(), rand.Int64())
	res, err := c.rdb.Eval(ctx, slidingWindowScript, []string{key},
		time.Now().UnixMilli(), window.Milliseconds(), limit, member,
	).Result()
	if err != nil {
		return false, 0, fmt.Errorf("sliding window check %q: %w", key, err)
	}

	values, ok := res.([]any)
	if !ok || len(values) != 2 {
		return false, 0, fmt.Errorf("sliding window check %q: unexpected script result %#v", key, res)
	}
	allowedInt, _ := values[0].(int64)
	retryAfterMs, _ := values[1].(int64)
	return allowedInt == 1, time.Duration(retryAfterMs) * time.Millisecond, nil
}

// DeleteByPrefix removes every key matching prefix+"*" — multitenancy-
// internals.md §12's tenant cache flush, used by OffboardTenantWorkflow's
// FlushTenantCache step. Uses SCAN rather than KEYS so it doesn't block the
// Redis event loop on a large keyspace; not an error if no keys match.
func (c *Client) DeleteByPrefix(ctx context.Context, prefix string) error {
	iter := c.rdb.Scan(ctx, 0, prefix+"*", 0).Iterator()

	var keys []string
	for iter.Next(ctx) {
		keys = append(keys, iter.Val())
		if len(keys) >= 500 {
			if err := c.rdb.Del(ctx, keys...).Err(); err != nil {
				return fmt.Errorf("delete keys matching %q*: %w", prefix, err)
			}
			keys = keys[:0]
		}
	}
	if err := iter.Err(); err != nil {
		return fmt.Errorf("scan keys matching %q*: %w", prefix, err)
	}

	if len(keys) > 0 {
		if err := c.rdb.Del(ctx, keys...).Err(); err != nil {
			return fmt.Errorf("delete keys matching %q*: %w", prefix, err)
		}
	}

	return nil
}
