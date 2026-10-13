package engine

import (
	"encoding/json/v2"
	"strings"
	"testing"
	"time"
)

func TestSSEBatchAndCursor(t *testing.T) {
	for _, done := range []bool{false, true} {
		response := Events(Event{ID: "next", Data: map[string]string{"line": "one"}})
		if done {
			response = EventsDone(Event{ID: "next", Data: map[string]string{"line": "one"}})
		}
		body, err := json.Marshal(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		var batch struct {
			Events []Event `json:"events"`
			Done   bool    `json:"done"`
		}
		if err := json.Unmarshal(body, &batch); err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != 200 || len(batch.Events) != 1 || batch.Events[0].ID != "next" || batch.Done != done {
			t.Fatalf("batch=%s", body)
		}
	}
	body, err := json.Marshal(Events().Body)
	if err != nil || string(body) != `{"events":[],"done":false}` {
		t.Fatalf("empty batch=%s err=%v", body, err)
	}
	req := &Request{Headers: map[string][]string{"last-event-id": {"cursor"}}}
	if req.LastEventID() != "cursor" {
		t.Fatal(req.LastEventID())
	}
}

func TestSSEPollIntervalValidation(t *testing.T) {
	saved := DefaultRouter
	t.Cleanup(func() { DefaultRouter = saved })
	for _, interval := range []time.Duration{0, 499 * time.Millisecond, 500 * time.Millisecond, time.Minute, time.Minute + time.Nanosecond} {
		DefaultRouter = NewRouter()
		Group("/group").SSE("/events", func(*Request) *Response { return Events() }, PollEvery(interval))
		err := ValidateRoutes()
		valid := interval >= 500*time.Millisecond && interval <= time.Minute
		if (err == nil) != valid {
			t.Fatalf("interval=%s error=%v", interval, err)
		}
		if valid && routeDeclarations(DefaultRouter.routes)[0].PollIntervalMs != int(interval/time.Millisecond) {
			t.Fatal("poll interval lost")
		}
	}
	DefaultRouter = NewRouter()
	SSE("/default", func(*Request) *Response { return Events() })
	if err := ValidateRoutes(); err != nil {
		t.Fatal(err)
	}
	if routeDeclarations(DefaultRouter.routes)[0].PollIntervalMs != 2000 {
		t.Fatal("default interval")
	}
	DefaultRouter = NewRouter()
	Group("/shared", PollEvery(time.Second)).SSE("/stream", func(*Request) *Response { return Events() })
	if routeDeclarations(DefaultRouter.routes)[0].PollIntervalMs != 1000 {
		t.Fatal("group interval ignored")
	}
	GET("/ordinary", func(*Request) *Response { return OK(nil) }, PollEvery(time.Second))
	if err := ValidateRoutes(); err == nil || !strings.Contains(err.Error(), "requires an SSE route") {
		t.Fatalf("error=%v", err)
	}
}
