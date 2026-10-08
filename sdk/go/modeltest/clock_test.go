package modeltest

import (
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func TestWithClock_GuestWalltimeAndRouteTimeout(t *testing.T) {
	t.Chdir("testdata/clockfixture")
	initialTime := time.Date(2040, 1, 2, 3, 4, 5, 0, time.UTC)
	var clockNS atomic.Int64
	clockNS.Store(initialTime.UnixNano())
	h := NewHarness(t, WithClock(func() time.Time { return time.Unix(0, clockNS.Load()) }))

	var previousRandom string
	for _, acceptedAt := range []time.Time{initialTime, initialTime.Add(time.Hour)} {
		clockNS.Store(acceptedAt.UnixNano())
		response := h.GET("/clockprobe/sample")
		if response.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, error = %v", response.StatusCode, response.JSON("error"))
		}

		for _, key := range []string{"before", "after", "requested_at"} {
			if got := response.JSON(key); got != acceptedAt.Format(time.RFC3339Nano) {
				t.Errorf("%s = %v, want %v", key, got, acceptedAt)
			}
		}
		if valid := response.JSON("library_valid"); valid != true {
			t.Error("JWT library did not use the injected clock")
		}
		random, _ := response.JSON("random").(string)
		if len(random) != 64 || random == previousRandom {
			t.Errorf("random = %q, previous = %q", random, previousRandom)
		}
		previousRandom = random
	}

	started := time.Now()
	response := h.GET("/clockprobe/sample", WithQuery("sleep_ms", "3600000"))
	if response.StatusCode != http.StatusServiceUnavailable || response.JSON("error.code") != "computation_limit_exceeded" {
		t.Fatalf("sleeping request: status = %d, error = %v", response.StatusCode, response.JSON("error"))
	}

	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Errorf("route timeout took %v, want prompt cancellation", elapsed)
	}

	if response := h.GET("/clockprobe/sample"); response.StatusCode != http.StatusOK {
		t.Errorf("request after timeout: status = %d, error = %v", response.StatusCode, response.JSON("error"))
	}
}
