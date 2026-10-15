package engine

import (
	"encoding/json/v2"
	"reflect"
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/ui"
)

func TestUIResponseWire(t *testing.T) {
	tests := []struct {
		name   string
		resp   *Response
		body   string
		header string
	}{
		{
			name:   "data and ordered instructions",
			resp:   UIResponse(map[string]string{"id": "inv1"}, ui.Toast("Saved", ui.Success), ui.Reload()),
			body:   `{"data":{"id":"inv1"},"ui":[{"type":"toast","message":"Saved","severity":"success"},{"type":"reload"}]}`,
			header: "1",
		},
		{
			name:   "instructions without data",
			resp:   UIInstructions(ui.CloseModal(), ui.Navigate("/invoices")),
			body:   `{"ui":[{"type":"close_modal"},{"type":"navigate","path":"/invoices"}]}`,
			header: "1",
		},
		{
			name:   "explicit null data",
			resp:   UIResponse(nil),
			body:   `{"data":null,"ui":[]}`,
			header: "1",
		},
		{
			name:   "empty instructions",
			resp:   UIInstructions(),
			body:   `{"ui":[]}`,
			header: "1",
		},
		{
			name: "plain data containing ui",
			resp: OK(map[string]any{"ui": "record field", "data": "record data"}),
			body: `{"ui":"record field","data":"record data"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wire := writeAndRead(t, tt.resp)
			if wire.StatusCode != 200 || wire.Headers["X-GoERP-UI"] != tt.header {
				t.Errorf("response = %+v, want status 200 and UI header %q", wire, tt.header)
			}

			var got, want any
			if err := json.Unmarshal(wire.Body, &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(tt.body), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("body = %s, want %s", wire.Body, tt.body)
			}
		})
	}
}
