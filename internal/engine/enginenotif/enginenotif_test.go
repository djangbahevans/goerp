package enginenotif

import (
	"encoding/json/v2"
	"maps"
	"strings"
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

func TestDefaultRows_HaveOneInAppRowPerShippedTemplate(t *testing.T) {
	rows, err := DefaultRows()
	if err != nil {
		t.Fatalf("DefaultRows() error: %v", err)
	}
	got := map[string]string{}
	for _, r := range rows {
		got[r.TemplateKey+"/"+r.Channel+"/"+r.Locale] = r.Fields["title_template"]
	}
	for _, key := range []string{
		"engine.activity_assigned/in_app/en", "engine.activity_due/in_app/en",
		"engine.record_mention/in_app/en", "engine.record_message/in_app/en",
	} {
		if got[key] == "" {
			t.Errorf("DefaultRows() has no title for %s: %v", key, got)
		}
	}
	emails := map[string]notiftemplate.Row{}
	for _, r := range rows {
		if r.Channel == notifications.ChannelEmail {
			emails[r.TemplateKey] = r
		}
	}
	for _, key := range []string{"engine.record_mention", "engine.record_message"} {
		r := emails[key]
		for _, col := range []string{notiftemplate.ColSubject, notiftemplate.ColHTML, notiftemplate.ColText} {
			if r.Fields[col] == "" {
				t.Errorf("DefaultRows() %s email row has no %s", key, col)
			}
		}
	}
	if len(rows) != 6 {
		t.Errorf("DefaultRows() = %d rows, want 6", len(rows))
	}
}

func TestTemplates_CommentContent(t *testing.T) {
	data := map[string]any{
		"AuthorName": "Kwame Mensah", "RecordName": "SO-0007", "ModelLabel": "Sales order",
		"Body": "@Ama Owusu can you confirm \"Friday\"?\nThanks", "Excerpt": "@Ama Owusu can you…",
		"ActionURL": "https://acme.example/_m/sales/orders/1", "TenantName": "Acme",
	}
	tests := []struct {
		name, recordName string
		want             inApp
		subject          string
	}{
		{RecordMention, "SO-0007", inApp{Title: "Kwame Mensah mentioned you on SO-0007", Body: "@Ama Owusu can you…", Icon: "at-sign"}, "Kwame Mensah mentioned you on SO-0007"},
		{RecordMessage, "SO-0007", inApp{Title: "Kwame Mensah on SO-0007", Body: "@Ama Owusu can you…", Icon: "message-square"}, "Kwame Mensah on SO-0007"},
		{RecordMessage, "", inApp{Title: "Kwame Mensah on Sales order", Body: "@Ama Owusu can you…", Icon: "message-square"}, "Kwame Mensah on Sales order"},
	}
	for _, tt := range tests {
		vars := maps.Clone(data)
		vars["RecordName"] = tt.recordName
		escaped := maps.Clone(vars)
		escaped["Body"] = `@Ama Owusu can you confirm \"Friday\"?\nThanks`

		var got inApp
		renderJSON(t, tt.name, notifications.ChannelInApp, escaped, &got)
		if got != tt.want {
			t.Errorf("%s in_app with record name %q = %+v, want %+v", tt.name, tt.recordName, got, tt.want)
		}
		var subject struct {
			Subject string `json:"subject"`
		}
		renderJSON(t, tt.name, notiftemplate.ChannelEmailSubject, escaped, &subject)
		if subject.Subject != tt.subject {
			t.Errorf("%s email subject = %q, want %q", tt.name, subject.Subject, tt.subject)
		}

		html := render(t, tt.name, notifications.ChannelEmail, vars)
		for _, want := range []string{"can you confirm &#34;Friday&#34;?\nThanks", `href="https://acme.example/_m/sales/orders/1"`} {
			if !strings.Contains(html, want) {
				t.Errorf("%s email html = %q, want it to contain %q", tt.name, html, want)
			}
		}
		if text := render(t, tt.name, notiftemplate.ChannelEmailText, vars); !strings.Contains(text, "can you confirm \"Friday\"?\nThanks") {
			t.Errorf("%s email text = %q, want the whole comment", tt.name, text)
		}
	}
}

func render(t *testing.T, name, channel string, vars map[string]any) string {
	t.Helper()
	locale, tmpl, ok := Templates().Resolve(name, channel, "en")
	if !ok {
		t.Fatalf("%s %s template did not resolve", name, channel)
	}
	rendered, err := notiftemplate.Render(tmpl, locale, vars)
	if err != nil {
		t.Fatalf("Render(%s %s) error: %v", name, channel, err)
	}
	return rendered
}

func renderJSON(t *testing.T, name, channel string, vars map[string]any, out any) {
	t.Helper()
	rendered := render(t, name, channel, vars)
	if err := json.Unmarshal([]byte(rendered), out); err != nil {
		t.Fatalf("%s %s output is not JSON: %v\n%s", name, channel, err, rendered)
	}
}
