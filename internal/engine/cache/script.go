package cache

import (
	"context"
	"fmt"
)

// Eval executes a Redis script atomically against the supplied keys.
func (c *Client) Eval(ctx context.Context, script string, keys []string, args ...any) (any, error) {
	result, err := c.rdb.Eval(ctx, script, keys, args...).Result()
	if err != nil {
		return nil, fmt.Errorf("execute Redis script: %w", err)
	}

	return result, nil
}
