package manifest

import (
	"encoding/json/v2"
	"reflect"
	"testing"
)

func marshalToMap(t *testing.T, v any) map[string]any {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal(%T) error: %v", v, err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("Unmarshal(%s) error: %v", data, err)
	}
	return m
}

func TestZeroValueViewTypesOmitBooleanAndNumericMembers(t *testing.T) {
	tests := []struct {
		name string
		v    any
		want map[string]any
	}{
		{"View", View{}, map[string]any{"name": "", "type": "", "resource": "", "label": ""}},
		{"ListColumn", ListColumn{}, map[string]any{"field": ""}},
		{"Filter", Filter{}, map[string]any{"field": ""}},
		{"FilterOption", FilterOption{}, map[string]any{"value": "", "label": ""}},
		{"BulkAction", BulkAction{}, map[string]any{"label": "", "type": ""}},
		{"ConfirmDialog", ConfirmDialog{}, map[string]any{"title": "", "message": ""}},
		{"FormTab", FormTab{}, map[string]any{}},
		{"FormSection", FormSection{}, map[string]any{}},
		{"FormField", FormField{}, map[string]any{"field": ""}},
		{"FormSidebar", FormSidebar{}, map[string]any{}},
		{"NavItem", NavItem{}, map[string]any{"label": "", "route": ""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := marshalToMap(t, tt.v); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("zero %s = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestDefaultTrueBooleanMembersRoundTripExplicitValues(t *testing.T) {
	ptr := func(b bool) *bool { return &b }

	tests := []struct {
		name   string
		member string
		unset  any
		with   func(*bool) any
		read   func(any) *bool
	}{
		{"View.selectable", "selectable", View{},
			func(b *bool) any { return View{Selectable: b} },
			func(v any) *bool { return v.(View).Selectable }},
		{"View.chatter", "chatter", View{},
			func(b *bool) any { return View{Chatter: b} },
			func(v any) *bool { return v.(View).Chatter }},
		{"ListColumn.truncate", "truncate", ListColumn{},
			func(b *bool) any { return ListColumn{Truncate: b} },
			func(v any) *bool { return v.(ListColumn).Truncate }},
		{"FormField.open_in_new_tab", "open_in_new_tab", FormField{},
			func(b *bool) any { return FormField{OpenInNewTab: b} },
			func(v any) *bool { return v.(FormField).OpenInNewTab }},
		{"CronJob.enabled_by_default", "enabled_by_default", CronJob{},
			func(b *bool) any { return CronJob{EnabledByDefault: b} },
			func(v any) *bool { return v.(CronJob).EnabledByDefault }},
		{"CronJob.per_tenant", "per_tenant", CronJob{},
			func(b *bool) any { return CronJob{PerTenant: b} },
			func(v any) *bool { return v.(CronJob).PerTenant }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, ok := marshalToMap(t, tt.unset)[tt.member]; ok {
				t.Errorf("unset %s is serialized, want it omitted", tt.member)
			}

			for _, want := range []bool{true, false} {
				data, err := json.Marshal(tt.with(ptr(want)))
				if err != nil {
					t.Fatalf("Marshal error: %v", err)
				}
				var m map[string]any
				if err := json.Unmarshal(data, &m); err != nil {
					t.Fatalf("Unmarshal error: %v", err)
				}
				if m[tt.member] != want {
					t.Errorf("%s = %v after marshal, want %v", tt.member, m[tt.member], want)
				}

				decoded := reflect.New(reflect.TypeOf(tt.unset))
				if err := json.Unmarshal(data, decoded.Interface()); err != nil {
					t.Fatalf("Unmarshal into %T error: %v", tt.unset, err)
				}
				got := tt.read(decoded.Elem().Interface())
				if got == nil || *got != want {
					t.Errorf("%s decoded = %v, want %v", tt.member, got, want)
				}
			}
		})
	}
}
