package engine

import (
	"encoding/json/v2"
	"testing"
	"time"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
)

func writeAndRead(t *testing.T, resp *Response) abi.Response {
	t.Helper()
	packed := WriteResponse(resp)
	var wire abi.Response
	if err := unmarshal(ReadMem(uint32(packed>>32), uint32(packed)), &wire); err != nil {
		t.Fatalf("unmarshal(response) error: %v", err)
	}
	return wire
}

type responseBodyContact struct {
	ID        string    `json:"id"`
	Note      string    `json:"note,omitempty"`
	Secret    string    `json:"-"`
	Email     *string   `json:"email"`
	UpdatedAt time.Time `json:"updated_at"`
}

func TestWriteResponse_EncodesBodyByItsJSONTags(t *testing.T) {
	body := responseBodyContact{ID: "c1", Secret: "s", UpdatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	wire := writeAndRead(t, OK(body))

	if wire.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200", wire.StatusCode)
	}
	if got, want := string(wire.Body), `{"id":"c1","email":null,"updated_at":"2026-01-02T03:04:05Z"}`; got != want {
		t.Errorf("Body = %s, want %s", got, want)
	}
}

func TestWriteResponse_MapBodyMatchesJSONMarshal(t *testing.T) {
	body := map[string]any{"data": []any{map[string]any{"id": "w1"}}}
	wire := writeAndRead(t, OK(body))

	want, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if string(wire.Body) != string(want) {
		t.Errorf("Body = %s, want %s", wire.Body, want)
	}
}

func TestWriteResponse_EscapesHTMLAndJSSeparators(t *testing.T) {
	wire := writeAndRead(t, OK(map[string]string{"html": "<b>&\u2028"}))
	if got, want := string(wire.Body), `{"html":"\u003cb\u003e\u0026\u2028"}`; got != want {
		t.Errorf("Body = %s, want %s", got, want)
	}
}

func TestWriteResponse_NilBodySendsNoBody(t *testing.T) {
	wire := writeAndRead(t, NoContent())
	if wire.StatusCode != 204 || wire.Body != nil {
		t.Errorf("response = %+v, want status 204 with no body", wire)
	}
}

func TestWriteResponse_UnencodableBodyBecomes500(t *testing.T) {
	wire := writeAndRead(t, OK(map[string]any{"ch": make(chan int)}))

	if wire.StatusCode != 500 {
		t.Fatalf("StatusCode = %d, want 500", wire.StatusCode)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(wire.Body, &body); err != nil {
		t.Fatalf("Body %s isn't JSON: %v", wire.Body, err)
	}
	if body.Error.Code != "engine.marshal_failed" {
		t.Errorf("error code = %q, want engine.marshal_failed", body.Error.Code)
	}
}
