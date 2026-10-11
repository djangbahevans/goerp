package abi

import "testing"

func TestApplicationErrorStatus(t *testing.T) {
	tests := []struct {
		code     string
		fallback int
		want     int
	}{
		{"contacts.contact.not_found", 500, 404},
		{"contacts.contact.forbidden", 500, 403},
		{"contacts.contact.duplicate_email", 500, 409},
		{"contacts.contact.self_reference", 500, 409},
		{"contacts.contact.circular_parent", 500, 409},
		{"contacts.contact.already_archived", 500, 409},
		{"sales.order.invalid_state_transition", 422, 409},
		{"contacts.contact.invalid_email", 500, 422},
		{"contacts.contact.missing_name", 500, 422},
		{"contacts.contact.limit_exceeded", 500, 422},
		{"contacts.connector.unavailable", 500, 503},
		{"contacts.import.invalid_csv", 500, 422},
		{"contacts.import.row_validation_failed", 500, 422},
		{"contacts.contact.row_validation_failed", 409, 409},
		{"sales.order.confirm_not_allowed", 403, 403},
		{"sales.order.custom_condition", 200, 500},
		{"sales.order.custom_condition", 600, 500},
		{"invalid", 403, 500},
		{"sales.order", 403, 500},
		{"sales.order.custom.extra", 403, 500},
	}

	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			if got := ApplicationErrorStatus(tt.code, tt.fallback); got != tt.want {
				t.Errorf("status = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestFrameworkErrorStatus(t *testing.T) {
	for code, want := range map[string]int{
		"invalid_body":            400,
		"engine.invalid_request":  400,
		"engine.route_not_found":  404,
		"engine.marshal_failed":   500,
		"orm.validation_failed":   422,
		"engine.custom.not_found": 0,
		"orm.custom_error":        0,
		"internal_error":          0,
	} {
		t.Run(code, func(t *testing.T) {
			if got := FrameworkErrorStatus(code); got != want {
				t.Errorf("status = %d, want %d", got, want)
			}
		})
	}
}
