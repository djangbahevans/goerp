// Package search wraps the engine's Meilisearch client. Meilisearch is
// extended-design scope (erp-design.md §1.1.1, §6.4 — Postgres trigram
// search serves this role in the initial build), so a Client here is
// optional: callers only construct one when GOERP_MEILISEARCH_URL is set,
// and a connectivity failure is expected to warn, not halt startup.
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
