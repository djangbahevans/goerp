package recordshares

import (
	"errors"
	"sync"
	"testing"
)

func TestGrantOwned_ConcurrentCreatorsCannotTakeOverTheGrant(t *testing.T) {
	store, _, slug := openTestStore(t)
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, creator := range []string{"00000000-0000-0000-0000-0000000000aa", "00000000-0000-0000-0000-0000000000bb"} {
		wg.Go(func() {
			_, _, err := store.GrantOwned(t.Context(), slug, "test.widget", "11111111-1111-1111-1111-111111111111", "00000000-0000-0000-0000-0000000000cc", "read", creator, nil)
			results <- err
		})
	}
	wg.Wait()
	close(results)

	succeeded, denied := 0, 0
	for err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrNotOwner):
			denied++
		default:
			t.Fatalf("GrantOwned: %v", err)
		}
	}
	if succeeded != 1 || denied != 1 {
		t.Fatalf("succeeded = %d, denied = %d, want one each", succeeded, denied)
	}
}
