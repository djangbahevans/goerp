package ui_test

import (
	"encoding/json/v2"
	"reflect"
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/ui"
)

func TestInstructionJSON(t *testing.T) {
	tests := []struct {
		name        string
		instruction ui.Instruction
		want        string
	}{
		{
			name:        "toast default duration",
			instruction: ui.Toast("Saved", ui.Success),
			want:        `{"type":"toast","message":"Saved","severity":"success"}`,
		},
		{
			name:        "persistent toast",
			instruction: ui.Toast("Failed", ui.Error, ui.WithDuration(0)),
			want:        `{"type":"toast","message":"Failed","severity":"error","duration_ms":0}`,
		},
		{
			name:        "toast duration override",
			instruction: ui.Toast("Low stock", ui.Warning, ui.WithDuration(8000)),
			want:        `{"type":"toast","message":"Low stock","severity":"warning","duration_ms":8000}`,
		},
		{
			name:        "info toast last option wins",
			instruction: ui.Toast("Ready", ui.Info, ui.WithDuration(8000), ui.WithDuration(1000)),
			want:        `{"type":"toast","message":"Ready","severity":"info","duration_ms":1000}`,
		},
		{
			name:        "current view reload",
			instruction: ui.Reload(),
			want:        `{"type":"reload"}`,
		},
		{
			name:        "record reload",
			instruction: ui.Reload(ui.WithTarget("sales.invoice", "inv1")),
			want:        `{"type":"reload","resource":"sales.invoice","record_id":"inv1"}`,
		},
		{
			name:        "list reload",
			instruction: ui.Reload(ui.WithListTarget("sales.invoice")),
			want:        `{"type":"reload","resource":"sales.invoice"}`,
		},
		{
			name:        "list target replaces record target",
			instruction: ui.Reload(ui.WithTarget("sales.invoice", "inv1"), ui.WithListTarget("crm.contact")),
			want:        `{"type":"reload","resource":"crm.contact"}`,
		},
		{
			name:        "badge count",
			instruction: ui.SetBadge("/hr/leave/approvals/count", 4),
			want:        `{"type":"set_badge","target":"/hr/leave/approvals/count","count":4}`,
		},
		{
			name:        "clear badge",
			instruction: ui.SetBadge("/orders/count-for-contact/c1", 0),
			want:        `{"type":"set_badge","target":"/orders/count-for-contact/c1","count":0}`,
		},
		{
			name:        "navigation leaves interpolation to shell",
			instruction: ui.Navigate("/sales/invoices/{record.invoice_id}"),
			want:        `{"type":"navigate","path":"/sales/invoices/{record.invoice_id}"}`,
		},
		{
			name:        "new record form",
			instruction: ui.OpenForm("sales.invoice"),
			want:        `{"type":"open_form","resource":"sales.invoice"}`,
		},
		{
			name:        "existing record form",
			instruction: ui.OpenForm("sales.invoice", ui.WithRecordID("inv1")),
			want:        `{"type":"open_form","resource":"sales.invoice","record_id":"inv1"}`,
		},
		{
			name: "dialog shell defaults",
			instruction: ui.OpenDialog(ui.Dialog{
				Title:  "Notes",
				Action: ui.Action{Method: "POST", Path: "/sales/invoices/inv1/notes"},
				Fields: []ui.Field{{Name: "notes", Label: "Notes", Type: ui.FieldTextarea}},
			}),
			want: `{"type":"open_dialog","title":"Notes","action":{"method":"POST","path":"/sales/invoices/inv1/notes"},"fields":[{"name":"notes","label":"Notes","type":"textarea"}]}`,
		},
		{
			name: "dialog fields and initial values",
			instruction: ui.OpenDialog(ui.Dialog{
				Title:       "Record Payment",
				SubmitLabel: "Record",
				Action:      ui.Action{Method: "POST", Path: "/sales/invoices/inv1/payments"},
				Fields: []ui.Field{
					{Name: "amount", Label: "Amount", Type: ui.FieldMonetary, Required: true, Default: "{record.amount_due}"},
					{
						Name: "method", Label: "Method", Type: ui.FieldSelect, Required: true,
						Placeholder: "Choose a method", Help: "Payment provider",
						Options: []ui.SelectOption{{Value: "cash", Label: "Cash"}, {Value: "bank", Label: "Bank"}},
					},
					{Name: "date", Label: "Date", Type: ui.FieldDate, Default: "{today}"},
					{Name: "phone", Label: "Phone", Type: ui.FieldPhone, Default: "{record.customer.phone}"},
					{Name: "count", Label: "Count", Type: ui.FieldNumber},
					{Name: "paid", Label: "Paid", Type: ui.FieldBoolean},
					{Name: "reference", Label: "Reference", Type: ui.FieldText},
				},
				Values: map[string]any{"amount": 0, "paid": false, "reference": ""},
			}),
			want: `{"type":"open_dialog","title":"Record Payment","submit_label":"Record","action":{"method":"POST","path":"/sales/invoices/inv1/payments"},"fields":[
				{"name":"amount","label":"Amount","type":"monetary","required":true,"default":"{record.amount_due}"},
				{"name":"method","label":"Method","type":"select","required":true,"placeholder":"Choose a method","help":"Payment provider","options":[{"value":"cash","label":"Cash"},{"value":"bank","label":"Bank"}]},
				{"name":"date","label":"Date","type":"date","default":"{today}"},
				{"name":"phone","label":"Phone","type":"phone","default":"{record.customer.phone}"},
				{"name":"count","label":"Count","type":"number"},
				{"name":"paid","label":"Paid","type":"boolean"},
				{"name":"reference","label":"Reference","type":"text"}
			],"values":{"amount":0,"paid":false,"reference":""}}`,
		},
		{
			name: "empty dialog collections",
			instruction: ui.OpenDialog(ui.Dialog{
				Title: "Confirm", Action: ui.Action{Method: "POST", Path: "/confirm"},
				Values: map[string]any{},
			}),
			want: `{"type":"open_dialog","title":"Confirm","action":{"method":"POST","path":"/confirm"},"fields":[]}`,
		},
		{
			name:        "close modal",
			instruction: ui.CloseModal(),
			want:        `{"type":"close_modal"}`,
		},
		{
			name:        "URL shell default tab",
			instruction: ui.OpenURL("https://checkout.example.test/pay"),
			want:        `{"type":"open_url","url":"https://checkout.example.test/pay"}`,
		},
		{
			name:        "URL explicit new tab",
			instruction: ui.OpenURL("https://example.test", ui.InNewTab),
			want:        `{"type":"open_url","url":"https://example.test","target":"blank"}`,
		},
		{
			name:        "URL current tab",
			instruction: ui.OpenURL("https://example.test", ui.InCurrentTab),
			want:        `{"type":"open_url","url":"https://example.test","target":"self"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.instruction)
			if err != nil {
				t.Fatal(err)
			}

			var gotValue, wantValue any
			if err := json.Unmarshal(got, &gotValue); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(tt.want), &wantValue); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(gotValue, wantValue) {
				t.Errorf("JSON = %s, want %s", got, tt.want)
			}
		})
	}
}
