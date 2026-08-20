// Package temporal wraps the engine's Temporal client, connecting eagerly
// at construction so a Stage 6 connectivity failure surfaces immediately
// rather than on first use. A connectivity failure here is warn-only at
// the Engine.New call site, matching search/storage rather than cache/db:
// a single unreachable Stage 6 dependency shouldn't halt engine startup.
package temporal

import (
	"context"
	"fmt"

	"github.com/caarlos0/env/v11"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
)

type Config struct {
	HostPort  string `env:"GOERP_TEMPORAL_HOST_PORT" envDefault:"localhost:7233"`
	Namespace string `env:"GOERP_TEMPORAL_NAMESPACE" envDefault:"default"`
}

type Client struct {
	sdk client.Client
}

func New(ctx context.Context) (*Client, error) {
	cfg := Config{}
	if err := env.Parse(&cfg); err != nil {
		return nil, fmt.Errorf("parse temporal config: %w", err)
	}

	c, err := client.DialContext(ctx, client.Options{HostPort: cfg.HostPort, Namespace: cfg.Namespace})
	if err != nil {
		return nil, fmt.Errorf("dial temporal at %q: %w", cfg.HostPort, err)
	}

	return &Client{sdk: c}, nil
}

func (c *Client) Close() { c.sdk.Close() }

func (c *Client) Ping(ctx context.Context) error {
	_, err := c.sdk.CheckHealth(ctx, &client.CheckHealthRequest{})
	return err
}

// HasPollers reports whether taskQueue currently has at least one
// registered poller — used to confirm a just-started workflow-worker has
// actually connected before Stage 6 continues (engine-internals.md §2
// step 30).
func (c *Client) HasPollers(ctx context.Context, taskQueue string) (bool, error) {
	resp, err := c.sdk.DescribeTaskQueue(ctx, taskQueue, enums.TASK_QUEUE_TYPE_WORKFLOW)
	if err != nil {
		return false, fmt.Errorf("describe task queue %q: %w", taskQueue, err)
	}
	return len(resp.GetPollers()) > 0, nil
}
