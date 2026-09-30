package enginenotif

import (
	"maps"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/djangbahevans/goerp/internal/engine/notifications"
	"github.com/djangbahevans/goerp/internal/engine/notiftemplate"
)

func TestTemplates_EveryDeclaredTemplateHasAnEnRow(t *testing.T) {
	for _, nt := range Types {
		for channel := range nt.Templates {
			defaultRow(t, nt.Name, channel)
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
		if got := renderInApp(t, tt.name, vars); got != tt.want {
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
	Title, Body, Icon string
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

		if got := renderInApp(t, tt.name, vars); got != tt.want {
			t.Errorf("%s in_app with record name %q = %+v, want %+v", tt.name, tt.recordName, got, tt.want)
		}
		if subject := render(t, tt.name, notifications.ChannelEmail, notiftemplate.ColSubject, vars); subject != tt.subject {
			t.Errorf("%s email subject = %q, want %q", tt.name, subject, tt.subject)
		}

		html := render(t, tt.name, notifications.ChannelEmail, notiftemplate.ColHTML, vars)
		for _, want := range []string{"can you confirm &#34;Friday&#34;?\nThanks", `href="https://acme.example/_m/sales/orders/1"`} {
			if !strings.Contains(html, want) {
				t.Errorf("%s email html = %q, want it to contain %q", tt.name, html, want)
			}
		}
		if text := render(t, tt.name, notifications.ChannelEmail, notiftemplate.ColText, vars); !strings.Contains(text, "can you confirm \"Friday\"?\nThanks") {
			t.Errorf("%s email text = %q, want the whole comment", tt.name, text)
		}
	}
}

// defaultRow is the en row DefaultRows has for name's channel template.
func defaultRow(t *testing.T, name, channel string) notiftemplate.Row {
	t.Helper()
	rows, err := DefaultRows()
	if err != nil {
		t.Fatalf("DefaultRows() error: %v", err)
	}
	i := slices.IndexFunc(rows, func(r notiftemplate.Row) bool {
		return r.TemplateKey == Module+"."+name && r.Channel == channel && r.Locale == "en"
	})
	if i < 0 {
		t.Fatalf("DefaultRows() has no en %s row for %s", channel, name)
	}
	return rows[i]
}

func render(t *testing.T, name, channel, col string, vars map[string]any) string {
	t.Helper()
	rendered, err := notiftemplate.RenderColumn(col, defaultRow(t, name, channel).Fields[col], vars)
	if err != nil {
		t.Fatalf("RenderColumn(%s %s) error: %v", name, col, err)
	}
	return rendered
}

func renderInApp(t *testing.T, name string, vars map[string]any) inApp {
	t.Helper()
	return inApp{
		Title: render(t, name, notifications.ChannelInApp, notiftemplate.ColTitle, vars),
		Body:  render(t, name, notifications.ChannelInApp, notiftemplate.ColBody, vars),
		Icon:  render(t, name, notifications.ChannelInApp, notiftemplate.ColIcon, vars),
	}
}
