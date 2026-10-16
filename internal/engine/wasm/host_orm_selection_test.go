package wasm

import (
	"testing"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

func TestValidateSelectionBeforeWrite(t *testing.T) {
	decl := *model.Define("contacts.contact").
		Field("type", model.Selection("person", "organization").Required()).
		Field("preferred_channel", model.Selection("phone", "sms", "whatsapp", "email"))

	for _, requireAll := range []bool{true, false} {
		for _, value := range []any{"company", "", 1} {
			hostErr := validateWriteFields(decl, map[string]any{"type": value}, requireAll)
			if hostErr == nil || hostErr.Code != abiv1.ErrCodeValidationFailed || hostErr.Details["field"] != "type" {
				t.Errorf("value %v: error = %+v", value, hostErr)
			}
		}
		for _, record := range []map[string]any{
			{"type": "person"},
			{"type": "organization", "preferred_channel": nil},
			{"type": "person", "preferred_channel": "sms"},
		} {
			if hostErr := validateWriteFields(decl, record, requireAll); hostErr != nil {
				t.Errorf("record %v: error = %+v", record, hostErr)
			}
		}
	}
}
