package fixture

import (
	"strconv"
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/modeltest"
)

func TestSSEOnePollAndCursor(t *testing.T) {
	h := modeltest.NewHarness(t)
	for _, done := range []bool{false, true} {
		response := h.WithHeader("Last-Event-ID", "client").GET("/widgets/events", modeltest.WithQuery("done", strconv.FormatBool(done)))
		if response.StatusCode != 200 {
			t.Fatalf("status=%d error=%s", response.StatusCode, response.ErrorCode())
		}
		var batch struct {
			Events []struct {
				ID   string `json:"id"`
				Data struct {
					After string `json:"after"`
				} `json:"data"`
			} `json:"events"`
			Done bool `json:"done"`
		}
		response.ParseJSON(&batch)
		if len(batch.Events) != 1 || batch.Events[0].ID != "next" || batch.Events[0].Data.After != "client" || batch.Done != done {
			t.Fatalf("batch=%+v", batch)
		}
	}
}
