package model

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/sdk/go/declare"
)

func TestDefineRegistersEveryBackend(t *testing.T) {
	Define("registry_test.table", Table("records")).WithStandardFields().Field("name", Text())
	Define("registry_test.virtual", Virtual())
	transient := Define("registry_test.initial", Transient(time.Minute))
	transient.Name = "registry_test.transient"
	Extend("registry_test.extension").Field("extra", Text())

	raw, err := declare.Export()
	if err != nil {
		t.Fatal(err)
	}

	var declarations map[string][]jsontext.Value
	if err := json.Unmarshal(raw, &declarations); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{`{"name":"registry_test.table"}`, `{"name":"registry_test.virtual"}`, `{"name":"registry_test.transient"}`} {
		if !slices.ContainsFunc(declarations[KindModel], func(raw jsontext.Value) bool { return string(raw) == want }) {
			t.Errorf("model declarations do not contain %s", want)
		}
	}
	if slices.ContainsFunc(declarations[KindModel], func(raw jsontext.Value) bool { return string(raw) == `{"name":"registry_test.extension"}` }) {
		t.Fatal("model extension registered as an owned model")
	}
}
