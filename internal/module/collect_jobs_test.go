package module

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/djangbahevans/goerp/sdk/go/jobs/def"
)

func decls(t *testing.T, byKind map[string][]any) Declarations {
	t.Helper()
	out := Declarations{}
	for kind, entries := range byKind {
		for _, e := range entries {
			raw, err := json.Marshal(e)
			if err != nil {
				t.Fatalf("encode %s declaration: %v", kind, err)
			}
			out[kind] = append(out[kind], jsontext.Value(raw))
		}
	}
	return out
}

func collectJSON(t *testing.T, c Collector, d Declarations) string {
	t.Helper()
	v, err := c.Collect(d)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if v == nil {
		return ""
	}
	out, err := json.Marshal(v, json.Deterministic(true))
	if err != nil {
		t.Fatalf("encode block: %v", err)
	}
	return string(out)
}

func TestJobTypesCollector_ProducesEntriesSortedByNameWithDefaults(t *testing.T) {
	d := decls(t, map[string][]any{
		def.KindJob: {
			def.JobDeclaration{Name: "z_report", Label: "Report"},
			def.JobDeclaration{
				Name: "a_import", Label: "Import", Description: "Imports", Queue: "bulk", TimeoutSeconds: 3600,
				MaxAttempts: 5, Priority: 70, UniqueBy: "file_id", PayloadStruct: true, PayloadFields: []string{"file_id"},
			},
		},
		def.KindJobHandler: {
			def.HandlerDeclaration{Name: "z_report", Handler: "handleReport"},
			def.HandlerDeclaration{Name: "a_import", Handler: "handleImport"},
		},
	})

	want := `[{"name":"a_import","label":"Import","handler":"handleImport","queue":"bulk","timeout_seconds":3600,"max_attempts":5,"unique_by":"file_id","description":"Imports","priority":70},` +
		`{"name":"z_report","label":"Report","handler":"handleReport","queue":"default"}]`
	if got := collectJSON(t, jobTypesCollector{}, d); got != want {
		t.Errorf("job_types =\n%s\nwant\n%s", got, want)
	}
}

func TestJobTypesCollector_NothingDeclaredLeavesTheKeyOut(t *testing.T) {
	if got := collectJSON(t, jobTypesCollector{}, Declarations{}); got != "[]" {
		t.Errorf("job_types = %s, want an empty list the framework leaves out", got)
	}
}

func TestJobTypesCollector_CrossReferenceFailures(t *testing.T) {
	job := func(name string) def.JobDeclaration { return def.JobDeclaration{Name: name, Label: name} }
	handler := func(name string) def.HandlerDeclaration { return def.HandlerDeclaration{Name: name, Handler: "h"} }

	tests := []struct {
		name string
		d    map[string][]any
		want string
	}{
		{
			name: "definition without a handler",
			d:    map[string][]any{def.KindJob: {job("orphan")}},
			want: `"orphan" is declared with jobs.Define but has no engine.HandleJob registration`,
		},
		{
			name: "handler for an undeclared definition",
			d:    map[string][]any{def.KindJobHandler: {handler("ghost")}},
			want: `engine.HandleJob is registered for "ghost", which the module never declared with jobs.Define`,
		},
		{
			name: "definition declared twice",
			d:    map[string][]any{def.KindJob: {job("twice"), job("twice")}, def.KindJobHandler: {handler("twice")}},
			want: `"twice" is declared more than once with jobs.Define`,
		},
		{
			name: "unique_by names a field the payload lacks",
			d: map[string][]any{
				def.KindJob:        {def.JobDeclaration{Name: "dedupe", Label: "D", UniqueBy: "missing", PayloadStruct: true, PayloadFields: []string{"present"}}},
				def.KindJobHandler: {handler("dedupe")},
			},
			want: `job "dedupe": jobs.UniqueBy("missing") names no field of its payload type (fields: [present])`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := jobTypesCollector{}.Collect(decls(t, tt.d))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Collect error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestJobTypesCollector_ReportsEveryProblemTogether(t *testing.T) {
	d := decls(t, map[string][]any{
		def.KindJob:        {def.JobDeclaration{Name: "a", Label: "A"}, def.JobDeclaration{Name: "b", Label: "B"}},
		def.KindJobHandler: {def.HandlerDeclaration{Name: "ghost", Handler: "h"}},
	})

	_, err := jobTypesCollector{}.Collect(d)
	if err == nil {
		t.Fatal("Collect succeeded")
	}
	for _, want := range []string{`"a" is declared`, `"b" is declared`, `"ghost"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %s", err, want)
		}
	}
}

func TestCronJobsCollector_ProducesEntriesWithEnabledFlag(t *testing.T) {
	d := decls(t, map[string][]any{
		def.KindCron: {
			def.CronDeclaration{Name: "weekly_scan", Label: "Scan", Schedule: "0 3 * * 0", Description: "Scans", TimeoutSeconds: 3600, Queue: "bulk"},
			def.CronDeclaration{Name: "a_off", Label: "Off", Schedule: "* * * * *", DisabledByDefault: true, TimeoutSeconds: 60, Queue: "default"},
		},
		def.KindCronHandler: {
			def.HandlerDeclaration{Name: "weekly_scan", Handler: "scanDuplicates"},
			def.HandlerDeclaration{Name: "a_off", Handler: "off"},
		},
	})

	want := `[{"name":"a_off","label":"Off","schedule":"* * * * *","handler":"off","enabled_by_default":false,"timeout_seconds":60,"queue":"default"},` +
		`{"name":"weekly_scan","label":"Scan","schedule":"0 3 * * 0","handler":"scanDuplicates","description":"Scans","enabled_by_default":true,"timeout_seconds":3600,"queue":"bulk"}]`
	if got := collectJSON(t, cronJobsCollector{}, d); got != want {
		t.Errorf("cron_jobs =\n%s\nwant\n%s", got, want)
	}
}

func TestCronJobsCollector_CrossReferenceFailures(t *testing.T) {
	cron := def.CronDeclaration{Name: "sweep", Label: "S", Schedule: "* * * * *", TimeoutSeconds: 60, Queue: "bulk"}

	_, err := cronJobsCollector{}.Collect(decls(t, map[string][]any{def.KindCron: {cron}}))
	if err == nil || !strings.Contains(err.Error(), `"sweep" is declared with jobs.DefineCron but has no engine.HandleCron registration`) {
		t.Errorf("a cron with no handler: %v", err)
	}

	_, err = cronJobsCollector{}.Collect(decls(t, map[string][]any{def.KindCronHandler: {def.HandlerDeclaration{Name: "ghost", Handler: "h"}}}))
	if err == nil || !strings.Contains(err.Error(), `engine.HandleCron is registered for "ghost", which the module never declared with jobs.DefineCron`) {
		t.Errorf("a handler for an undeclared cron: %v", err)
	}
}

func TestJobTypesCollector_UniqueByOnAMapPayloadIsNotChecked(t *testing.T) {
	d := decls(t, map[string][]any{
		def.KindJob:        {def.JobDeclaration{Name: "dedupe", Label: "D", UniqueBy: "key"}},
		def.KindJobHandler: {def.HandlerDeclaration{Name: "dedupe", Handler: "h"}},
	})

	if got := collectJSON(t, jobTypesCollector{}, d); !strings.Contains(got, `"unique_by":"key"`) {
		t.Errorf("job_types = %s, want unique_by kept for a payload whose keys are not known", got)
	}
}
