package route

import (
	"testing"
)

func TestParamsFromContext_ReturnsStoredParams(t *testing.T) {
	ctx := WithParams(t.Context(), map[string]string{"id": "abc123"})

	got := ParamsFromContext(ctx)
	if got["id"] != "abc123" {
		t.Errorf(`ParamsFromContext(ctx)["id"] = %q, want "abc123"`, got["id"])
	}
}

func TestParamsFromContext_EmptyMapWhenNeverStored(t *testing.T) {
	got := ParamsFromContext(t.Context())
	if got == nil {
		t.Error("ParamsFromContext() = nil, want a non-nil empty map")
	}
	if len(got) != 0 {
		t.Errorf("ParamsFromContext() = %v, want empty", got)
	}
}
