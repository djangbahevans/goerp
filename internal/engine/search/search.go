// Package search wraps optional Meilisearch access. Missing configuration or connection
// failures leave Postgres trigram search available without halting startup.
package search

import (
	"fmt"

	"github.com/meilisearch/meilisearch-go"
)

type Client struct {
	client meilisearch.ServiceManager
}

func New(url, key string) (*Client, error) {
	client := meilisearch.New(url, meilisearch.WithAPIKey(key))

	if _, err := client.Health(); err != nil {
		return nil, fmt.Errorf("connect to meilisearch: %w", err)
	}

	return &Client{client: client}, nil
}

func (c *Client) Ping() error {
	_, err := c.client.Health()
	return err
}

// DeleteIndex deletes the Meilisearch index identified by indexUID —
// multitenancy-internals.md §13's per-tenant index naming, used by
// OffboardTenantWorkflow's DeleteSearchIndex step. Not an error if the
// index doesn't exist (Meilisearch's own delete is idempotent for that
// case, returning a task that succeeds trivially).
func (c *Client) DeleteIndex(indexUID string) error {
	if _, err := c.client.DeleteIndex(indexUID); err != nil {
		return fmt.Errorf("delete index %q: %w", indexUID, err)
	}
	return nil
}
