package enginenotif

import (
	"encoding/json/v2"
	"maps"
	"testing"
	"testing/fstest"

	"github.com/djangbahevans/goerp/internal/engine/notifications"
	"github.com/djangbahevans/goerp/internal/engine/notiftemplate"
)

func TestTemplates_EveryDeclaredTemplateLoads(t *testing.T) {
	mt := Templates()
	for _, nt := range Types {
		for channel := range nt.Templates {
			if _, _, ok := mt.Resolve(nt.Name, channel, "en"); !ok {
				t.Errorf("%s %s template did not resolve for en", nt.Name, channel)
			}
		}
	}
}

func TestTemplates_ActivityInAppContent(t *testing.T) {
	data := map[string]any{
		"Summary": "Confirm Friday delivery", "TypeLabel": "Call", "TypeIcon": "phone",
		"RecordName": "SO-0007", "DueDate": "2026-09-25",
	}
	tests := []struct {
		name  string
		extra map[string]any
		want  inApp
	}{
		{ActivityAssigned, map[string]any{"AssignedByName": "Kwame Mensah"}, inApp{
			Title: "Kwame Mensah assigned you: Confirm Friday delivery", Body: "Call on SO-0007, due 2026-09-25", Icon: "phone",
		}},
		{ActivityAssigned, map[string]any{"AssignedByName": "", "RecordName": ""}, inApp{
			Title: "Assigned to you: Confirm Friday delivery", Body: "Call, due 2026-09-25", Icon: "phone",
		}},
		{ActivityDue, map[string]any{"Overdue": false}, inApp{
			Title: "Due today: Confirm Friday delivery", Body: "Call on SO-0007, due 2026-09-25", Icon: "phone",
		}},
		{ActivityDue, map[string]any{"Overdue": true}, inApp{
			Title: "Overdue: Confirm Friday delivery", Body: "Call on SO-0007, due 2026-09-25", Icon: "phone",
		}},
	}
	for _, tt := range tests {
		vars := maps.Clone(data)
		maps.Copy(vars, tt.extra)
		locale, tmpl, ok := Templates().Resolve(tt.name, notifications.ChannelInApp, "fr-GH")
		if !ok {
			t.Fatalf("%s in_app template did not resolve", tt.name)
		}
		rendered, err := notiftemplate.Render(tmpl, locale, vars)
		if err != nil {
			t.Fatalf("Render(%s) error: %v", tt.name, err)
		}
		var got inApp
		if err := json.Unmarshal([]byte(rendered), &got); err != nil {
			t.Fatalf("%s in_app output is not JSON: %v\n%s", tt.name, err, rendered)
		}
		if got != tt.want {
			t.Errorf("%s with %v = %+v, want %+v", tt.name, tt.extra, got, tt.want)
		}
	}
}

func TestLoadTemplates_RejectsAMissingEnVariant(t *testing.T) {
	fsys := fstest.MapFS{"templates/activity_assigned/in_app.fr.json": {Data: []byte(`{}`)}}
	if _, err := loadTemplates(fsys); err == nil {
		t.Fatal("loadTemplates() without an en variant: error = nil")
	}
}

type inApp struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Icon  string `json:"icon"`
}
