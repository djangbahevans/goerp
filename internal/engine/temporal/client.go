// Package temporal wraps the engine's Temporal client, connecting eagerly
// at construction so a Stage 6 connectivity failure surfaces immediately
// rather than on first use. A connectivity failure here is warn-only at
// the Engine.New call site, matching search/storage rather than cache/db:
// a single unreachable Stage 6 dependency shouldn't halt engine startup.
package temporal

import (
	"context"
	"fmt"
	"time"

	"github.com/caarlos0/env/v11"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

const (
	pollerConfirmTimeout  = 30 * time.Second
	pollerConfirmInterval = 200 * time.Millisecond
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

// WaitForPollers polls HasPollers until taskQueue has at least one
// registered poller, or returns an error after pollerConfirmTimeout (or
// if ctx is done first) — the shared "confirm a just-started worker
// actually connected" check both workflowworker.Manager (per-module
// workflow-worker processes) and systemworker.Worker (the engine's own
// in-process Temporal worker) use.
func (c *Client) WaitForPollers(ctx context.Context, taskQueue string) error {
	deadline := time.Now().Add(pollerConfirmTimeout)
	ticker := time.NewTicker(pollerConfirmInterval)
	defer ticker.Stop()

	for {
		has, err := c.HasPollers(ctx, taskQueue)
		if err != nil {
			return err
		}
		if has {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("no poller registered on %q after %s", taskQueue, pollerConfirmTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// NewWorker constructs a Temporal worker.Worker on taskQueue, using this
// Client's underlying connection — a thin factory so callers (currently
// just internal/engine/systemworker) never need their own import of
// go.temporal.io/sdk/client to build one.
func (c *Client) NewWorker(taskQueue string, options worker.Options) worker.Worker {
	return worker.New(c.sdk, taskQueue, options)
}

// ExecuteWorkflow starts workflow (a registered workflow function or its
// registered name) and returns a handle to the run. A thin passthrough to
// the underlying SDK client — options.ID and options.TaskQueue are the
// caller's (currently internal/engine/tenantprovision's) to set; this
// method makes no decision about workflow ID or reuse policy itself.
func (c *Client) ExecuteWorkflow(ctx context.Context, options client.StartWorkflowOptions, workflow any, args ...any) (client.WorkflowRun, error) {
	return c.sdk.ExecuteWorkflow(ctx, options, workflow, args...)
}
