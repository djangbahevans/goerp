package engine

import (
	"net/url"
	"slices"
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

func TestRequest_PathParam(t *testing.T) {
	req := &Request{PathParams: map[string]string{"email": "a+b/c@d.test"}}
	if got := req.PathParam("email"); got != "a+b/c@d.test" {
		t.Errorf(`PathParam("email") = %q, want "a+b/c@d.test"`, got)
	}
	if got := req.PathParam("missing"); got != "" {
		t.Errorf(`PathParam("missing") = %q, want ""`, got)
	}
	if got := (&Request{}).PathParam("email"); got != "" {
		t.Errorf(`PathParam on nil params = %q, want ""`, got)
	}
}

func TestRequest_QueryParam(t *testing.T) {
	req := &Request{QueryParams: url.Values{"page": {"2", "3"}}}
	if got := req.QueryParam("page"); got != "2" {
		t.Errorf(`QueryParam("page") = %q, want the first value "2"`, got)
	}
	if got := req.QueryParam("missing"); got != "" {
		t.Errorf(`QueryParam("missing") = %q, want ""`, got)
	}
	if got := (&Request{}).QueryParam("page"); got != "" {
		t.Errorf(`QueryParam on nil query = %q, want ""`, got)
	}
}

func TestRequest_QueryParamInt(t *testing.T) {
	req := &Request{QueryParams: url.Values{"limit": {"25"}, "neg": {"-4"}, "bad": {"abc"}, "empty": {""}}}
	tests := []struct {
		name string
		want int
	}{
		{"limit", 25},
		{"neg", -4},
		{"bad", 50},
		{"empty", 50},
		{"missing", 50},
	}
	for _, tc := range tests {
		if got := req.QueryParamInt(tc.name, 50); got != tc.want {
			t.Errorf("QueryParamInt(%q, 50) = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestRequest_QueryParamAll(t *testing.T) {
	req := &Request{QueryParams: url.Values{"ids": {"1", "2"}}}
	got := req.QueryParamAll("ids")
	if !slices.Equal(got, []string{"1", "2"}) {
		t.Fatalf(`QueryParamAll("ids") = %q, want ["1" "2"]`, got)
	}
	got[0] = "changed"
	if req.QueryParams["ids"][0] != "1" {
		t.Error("modifying QueryParamAll's result changed the request's query")
	}
	if got := req.QueryParamAll("missing"); got != nil {
		t.Errorf(`QueryParamAll("missing") = %q, want nil`, got)
	}
}

func TestRequest_HeaderIsCaseInsensitive(t *testing.T) {
	req := &Request{Headers: map[string][]string{
		"content-type": {"application/json"},
		"accept":       {"text/html", "application/json"},
	}}
	for _, name := range []string{"Content-Type", "content-type", "CONTENT-TYPE"} {
		if got := req.Header(name); got != "application/json" {
			t.Errorf("Header(%q) = %q, want application/json", name, got)
		}
	}
	if got := req.Header("Accept"); got != "text/html" {
		t.Errorf(`Header("Accept") = %q, want the first value text/html`, got)
	}
	if got := req.Header("missing"); got != "" {
		t.Errorf(`Header("missing") = %q, want ""`, got)
	}
}

func TestRequest_HeaderAll(t *testing.T) {
	req := &Request{Headers: map[string][]string{"accept": {"text/html", "application/json"}}}
	got := req.HeaderAll("Accept")
	if !slices.Equal(got, []string{"text/html", "application/json"}) {
		t.Fatalf(`HeaderAll("Accept") = %q, want both values in order`, got)
	}
	got[0] = "changed"
	if req.Headers["accept"][0] != "text/html" {
		t.Error("modifying HeaderAll's result changed the request's headers")
	}
	if got := req.HeaderAll("missing"); got != nil {
		t.Errorf(`HeaderAll("missing") = %q, want nil`, got)
	}
}
