package engine

import (
	"strings"
	"testing"
)

func TestRequest_RawBodyReturnsExactBytes(t *testing.T) {
	body := []byte(`{"hello":"world"}`)
	req := &Request{Body: body}

	got := req.RawBody()
	if string(got) != string(body) {
		t.Errorf("RawBody() = %q, want %q", got, body)
	}
}

func TestRequest_RawBodyReturnsEmptyForNilBody(t *testing.T) {
	req := &Request{}
	if got := req.RawBody(); len(got) != 0 {
		t.Errorf("RawBody() = %q, want empty", got)
	}
}

func TestRequest_ParseJSONUnmarshalsIntoTarget(t *testing.T) {
	req := &Request{Body: []byte(`{"name":"widget","count":3}`)}

	var v struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	if err := req.ParseJSON(&v); err != nil {
		t.Fatalf("ParseJSON() error: %v", err)
	}
	if v.Name != "widget" || v.Count != 3 {
		t.Errorf("ParseJSON() = %+v, want {Name:widget Count:3}", v)
	}
}

func TestRequest_ParseJSONMalformedReturnsDescriptiveError(t *testing.T) {
	req := &Request{Body: []byte(`{not valid json`)}

	var v map[string]any
	err := req.ParseJSON(&v)
	if err == nil {
		t.Fatal("ParseJSON() error = nil, want an error for malformed JSON")
	}
	if !strings.Contains(err.Error(), "parse json body") {
		t.Errorf("ParseJSON() error = %q, want it to describe the failed operation", err.Error())
	}
}

func TestRequest_ParseJSONRejectsDuplicateObjectMemberNames(t *testing.T) {
	req := &Request{Body: []byte(`{"name":"widget","name":"gadget"}`)}

	var v map[string]any
	err := req.ParseJSON(&v)
	if err == nil {
		t.Fatal("ParseJSON() error = nil, want an error for a duplicate object member name")
	}
}

func TestRequest_ParseJSONRejectsInvalidUTF8(t *testing.T) {
	req := &Request{Body: []byte("{\"name\":\"\xff\xfe\"}")}

	var v map[string]any
	err := req.ParseJSON(&v)
	if err == nil {
		t.Fatal("ParseJSON() error = nil, want an error for invalid UTF-8")
	}
}

func TestRequest_ParseJSONMatchesFieldNamesCaseSensitively(t *testing.T) {
	req := &Request{Body: []byte(`{"NAME":"widget"}`)}

	var v struct {
		Name string `json:"name"`
	}
	if err := req.ParseJSON(&v); err != nil {
		t.Fatalf("ParseJSON() error: %v", err)
	}
	if v.Name != "" {
		t.Errorf("ParseJSON() name = %q, want empty — a case-mismatched field name is unknown, not matched", v.Name)
	}
}

// TestRequest_RawBodyAndParseJSONBothReadTheSameUnderlyingBody proves
// calling one doesn't consume or mutate what the other sees — Body is a
// plain []byte field, not a stream, so both accessors are always safe to
// call on the same Request regardless of order or how many times.
func TestRequest_RawBodyAndParseJSONBothReadTheSameUnderlyingBody(t *testing.T) {
	req := &Request{Body: []byte(`{"name":"widget"}`)}

	raw1 := req.RawBody()
	var v struct {
		Name string `json:"name"`
	}
	if err := req.ParseJSON(&v); err != nil {
		t.Fatalf("ParseJSON() error: %v", err)
	}
	raw2 := req.RawBody()

	if string(raw1) != string(raw2) {
		t.Errorf("RawBody() changed across calls: %q then %q", raw1, raw2)
	}
	if v.Name != "widget" {
		t.Errorf("ParseJSON() name = %q, want %q", v.Name, "widget")
	}
}
