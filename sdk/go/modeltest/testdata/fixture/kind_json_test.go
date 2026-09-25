package fixture

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/modeltest"
)

// TestEnableOpsRoutes_SendEachFieldKindInItsJSONShape pins every column-backed
// field kind's JSON shape on the EnableOps routes (goerp#1165), which
// goerp codegen's generated model types describe.
func TestEnableOpsRoutes_SendEachFieldKindInItsJSONShape(t *testing.T) {
	h := modeltest.NewHarness(t)

	gadget := h.POST("/widgets/gadgets", map[string]any{"name": "Acme"})
	if gadget.StatusCode != 201 {
		t.Fatalf("create gadget status = %d; error=%v", gadget.StatusCode, gadget.JSON("error.message"))
	}
	gadgetID, _ := gadget.JSON("id").(string)

	body := map[string]any{
		"char_field":         "c",
		"text_field":         "t",
		"integer_field":      42,
		"bigint_field":       1234567890123,
		"float_field":        3.5,
		"decimal_field":      "123.45",
		"boolean_field":      true,
		"uuid_field":         "0190a6a0-0000-7000-8000-000000000001",
		"timestamptz_field":  "2024-03-15T10:30:00Z",
		"date_field":         "2024-03-15",
		"time_field":         "13:45:00",
		"jsonb_field":        map[string]any{"k": 1, "list": []any{1, "a"}},
		"bytea_field":        "aGVsbG8=",
		"selection_field":    "a",
		"enum_field":         "medium",
		"gadget_id":          gadgetID,
		"link_type":          "widgets.gadget",
		"dynamic_link_field": gadgetID,
	}
	want := map[string]any{
		"char_field":         "c",
		"text_field":         "t",
		"integer_field":      float64(42),
		"bigint_field":       float64(1234567890123),
		"float_field":        3.5,
		"decimal_field":      "123.45",
		"boolean_field":      true,
		"uuid_field":         "0190a6a0-0000-7000-8000-000000000001",
		"timestamptz_field":  "2024-03-15T10:30:00Z",
		"date_field":         "2024-03-15",
		"time_field":         "13:45:00",
		"jsonb_field":        map[string]any{"k": float64(1), "list": []any{float64(1), "a"}},
		"bytea_field":        "aGVsbG8=",
		"selection_field":    "a",
		"enum_field":         "medium",
		"gadget_id":          gadgetID,
		"sequence_field":     "KM-0001",
		"link_type":          "widgets.gadget",
		"dynamic_link_field": gadgetID,
	}
	check := func(t *testing.T, label string, got map[string]any) {
		t.Helper()
		for field, w := range want {
			if !reflect.DeepEqual(got[field], w) {
				t.Errorf("%s: %s = %#v, want %#v", label, field, got[field], w)
			}
		}
	}

	created := h.POST("/widgets/kind_matrices", body)
	if created.StatusCode != 201 {
		t.Fatalf("create status = %d; error=%v msg=%v", created.StatusCode, created.JSON("error.code"), created.JSON("error.message"))
	}
	var createdRec map[string]any
	created.ParseJSON(&createdRec)
	check(t, "create", createdRec)
	id, _ := createdRec["id"].(string)

	var got map[string]any
	h.GET(fmt.Sprintf("/widgets/kind_matrices/%s", id)).ParseJSON(&got)
	check(t, "get", got)
	if expansion, _ := got["gadget"].(map[string]any); expansion["id"] != gadgetID {
		t.Errorf("get: gadget expansion = %#v, want the related gadget's id", got["gadget"])
	}

	var list struct {
		Data []map[string]any `json:"data"`
	}
	h.GET("/widgets/kind_matrices").ParseJSON(&list)
	if len(list.Data) != 1 {
		t.Fatalf("list returned %d records, want 1", len(list.Data))
	}
	check(t, "list", list.Data[0])
}

func TestEnableOpsRoutes_JSONBHoldsAnyJSONValue(t *testing.T) {
	h := modeltest.NewHarness(t)

	for _, value := range []any{"just a string", float64(7), []any{"a", float64(1)}, false} {
		created := h.POST("/widgets/kind_matrices", map[string]any{"jsonb_field": value})
		if created.StatusCode != 201 {
			t.Fatalf("create with jsonb %#v: status = %d; error=%v", value, created.StatusCode, created.JSON("error.message"))
		}
		var rec map[string]any
		h.GET(fmt.Sprintf("/widgets/kind_matrices/%v", created.JSON("id"))).ParseJSON(&rec)
		if !reflect.DeepEqual(rec["jsonb_field"], value) {
			t.Errorf("jsonb_field = %#v, want %#v", rec["jsonb_field"], value)
		}
	}
}

func TestEnableOpsRoutes_RejectAByteaValueThatIsNotBase64(t *testing.T) {
	h := modeltest.NewHarness(t)

	resp := h.POST("/widgets/kind_matrices", map[string]any{"bytea_field": "not base64!"})
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400; body error=%v", resp.StatusCode, resp.JSON("error.message"))
	}
	if msg, _ := resp.JSON("error.message").(string); msg == "" || !strings.Contains(msg, "bytea_field") {
		t.Errorf("error message = %q, want it to name bytea_field", msg)
	}
}
