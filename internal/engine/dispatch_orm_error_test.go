package engine

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
)

func TestWriteHostErrorPreservesFieldDetails(t *testing.T) {
	for _, details := range []map[string]any{nil, {"field": "given_name"}} {
		recorder := httptest.NewRecorder()
		writeHostError(t.Context(), recorder, &abiv1.HostError{
			Code:    abiv1.ErrCodeValidationFailed,
			Message: "a person needs a given name or family name",
			Details: details,
		})
		if recorder.Code != http.StatusBadRequest {
			t.Fatalf("status = %d", recorder.Code)
		}

		var body struct {
			Error map[string]any `json:"error"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		got, present := body.Error["details"]
		if details == nil {
			if present {
				t.Errorf("unexpected details = %v", got)
			}
		} else if got.(map[string]any)["field"] != "given_name" {
			t.Errorf("details = %v", got)
		}
	}
}
