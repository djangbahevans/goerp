package loader

import (
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestValidateSequenceFormats(t *testing.T) {
	valid := model.Define("sales.order").Field("number", model.Sequence("SO/{year}/{seq:05}"))
	if err := validateSequenceFormats([]model.ModelDeclaration{*valid}); err != nil {
		t.Errorf("validateSequenceFormats(valid) = %v, want nil", err)
	}

	invalid := model.Define("sales.order").Field("number", model.Sequence("SO-{year}-"))
	err := validateSequenceFormats([]model.ModelDeclaration{*valid, *invalid})
	if err == nil {
		t.Fatal("validateSequenceFormats(format with no {seq:N}) = nil, want an error")
	}
	if !strings.Contains(err.Error(), "model sales.order: field number") {
		t.Errorf("error = %q, want it to name the model and field", err)
	}
}
