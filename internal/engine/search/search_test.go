package search

import "testing"

// localMeilisearch points at the compose.dev.yml Meilisearch instance
// (localhost:7700, master key from README.md's local development section).
const (
	localMeilisearchURL = "http://localhost:7700"
	localMeilisearchKey = "2f14b775804ecaf5dc4084d32aa034a7"
)

func TestNewConnectsAndHealthChecks(t *testing.T) {
	c, err := New(localMeilisearchURL, localMeilisearchKey)
	if err != nil {
		t.Skipf("meilisearch not reachable at %s (start compose.dev.yml): %v", localMeilisearchURL, err)
	}
	if c == nil {
		t.Fatal("New() returned a nil client with no error")
	}
}

func TestNewUnreachable(t *testing.T) {
	_, err := New("http://127.0.0.1:1", "irrelevant")
	if err == nil {
		t.Fatal("New() against an unreachable URL: expected an error, got nil")
	}
}

func TestDeleteIndex_NonexistentIndexIsNotAnError(t *testing.T) {
	c, err := New(localMeilisearchURL, localMeilisearchKey)
	if err != nil {
		t.Skipf("meilisearch not reachable at %s (start compose.dev.yml): %v", localMeilisearchURL, err)
	}

	if err := c.DeleteIndex("nonexistent-index-" + t.Name()); err != nil {
		t.Errorf("DeleteIndex() on a nonexistent index: error = %v, want nil", err)
	}
}
