package engine

import (
	"errors"
	"testing"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/orm"
)

func TestApplicationErrorConstructors(t *testing.T) {
	tests := []struct {
		name string
		resp *Response
		code string
		want int
	}{
		{"not found", NotFound("contacts.contact.not_found", nil), "contacts.contact.not_found", 404},
		{"conflict", Conflict("contacts.contact.duplicate_email", map[string]any{"email": "a@test"}), "contacts.contact.duplicate_email", 409},
		{"custom forbidden", Forbidden("sales.order.confirm_not_allowed", nil), "sales.order.confirm_not_allowed", 403},
		{"mapped status", Forbidden("sales.order.invalid_state_transition", nil), "sales.order.invalid_state_transition", 409},
		{"custom message", Error(418, "sales.order.custom", "custom message", nil), "sales.order.custom", 418},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wire := writeAndRead(t, tt.resp)
			if wire.StatusCode != tt.want || wire.ErrorKind != "" {
				t.Fatalf("wire = %+v, want application error status %d", wire, tt.want)
			}

			body := tt.resp.Body.(map[string]any)["error"].(map[string]any)
			if body["code"] != tt.code || body["message"] == "" {
				t.Errorf("error = %v, want code %q and a message", body, tt.code)
			}
			if tt.name == "conflict" && body["details"].(map[string]any)["email"] != "a@test" {
				t.Errorf("details = %v", body["details"])
			}
			if tt.name == "custom message" && body["message"] != "custom message" {
				t.Errorf("message = %v", body["message"])
			}
			if tt.name == "not found" {
				if _, exists := body["details"]; exists {
					t.Error("nil details must be omitted")
				}
			}
		})
	}
}

func TestSDKFrameworkErrorClassification(t *testing.T) {
	for name, resp := range map[string]*Response{
		"bad body":        BadRequest(errors.New("bad JSON")),
		"missing route":   notFound(),
		"invalid field":   invalidField(&orm.ValueError{Field: "name"}),
		"marshal failure": OK(make(chan int)),
	} {
		t.Run(name, func(t *testing.T) {
			if wire := writeAndRead(t, resp); wire.ErrorKind != abi.FrameworkError {
				t.Errorf("classification = %q, want framework", wire.ErrorKind)
			}
		})
	}

	packed := DispatchRequest(0, 0)
	var wire abi.Response
	if err := unmarshal(ReadMem(uint32(packed>>32), uint32(packed)), &wire); err != nil {
		t.Fatal(err)
	}
	if wire.ErrorKind != abi.FrameworkError || wire.StatusCode != 400 {
		t.Errorf("invalid request response = %+v", wire)
	}
}
