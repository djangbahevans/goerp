package module

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const jobsFixtureManifest = "{\n  \"name\": \"widgets\",\n  \"version\": \"1.0.0\"\n}\n"

// jobsFixtureMain declares one job, one cron job and one provider job, each
// with a handler, the way a module's cmd/module package does.
const jobsFixtureMain = `package main

import (
	"github.com/djangbahevans/goerp/sdk/go/engine"
	"github.com/djangbahevans/goerp/sdk/go/jobs"
)

type importPayload struct {
	FileID string ` + "`msgpack:\"file_id\"`" + `
}

type chargePayload struct{}

var (
	importJob = jobs.Define[importPayload]("widgets_import", jobs.Label("Import widgets"),
		jobs.Queue(jobs.QueueBulk), jobs.Timeout(600*time.Second), jobs.UniqueBy("file_id"))
	sweep  = jobs.DefineCron("widgets_sweep", jobs.Schedule("0 3 * * *"), jobs.Label("Sweep widgets"))
	charge = jobs.DefineProvider[chargePayload, struct{}]("payment_provider", "widgets_charge")
)

func handleImport(*engine.JobContext, importPayload) error { return nil }

func handleSweep(*jobs.CronContext) error { return nil }

func init() {
	engine.HandleJob(importJob, handleImport)
	engine.HandleCron(sweep, handleSweep)
	engine.HandleProviderJob(charge, func(*engine.JobContext, chargePayload) error { return nil })
}

func main() {}
`

func jobsFixtureSource(replacements ...string) string {
	src := jobsFixtureMain
	if !strings.Contains(src, `"time"`) {
		src = strings.Replace(src, "import (", "import (\n\t\"time\"\n", 1)
	}
	return strings.NewReplacer(replacements...).Replace(src)
}

func TestGenerate_JobsAndCronsProduceTheirManifestBlocks(t *testing.T) {
	dir := writeCollectFixture(t, jobsFixtureSource(), jobsFixtureManifest)
	ctx := generateCtx(t)

	result, err := Generate(ctx, dir, GenerateOptions{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got := strings.Join(result.Blocks, ","); got != "cron_jobs,job_types" {
		t.Errorf("Blocks = %s, want cron_jobs,job_types", got)
	}

	got := readFile(t, filepath.Join(dir, "manifest.json"))
	for _, want := range []string{
		`"name": "widgets_import"`, `"handler": "handleImport"`, `"queue": "bulk"`, `"timeout_seconds": 600`, `"unique_by": "file_id"`,
		`"name": "widgets_sweep"`, `"handler": "handleSweep"`, `"schedule": "0 3 * * *"`, `"enabled_by_default": true`, `"timeout_seconds": 3600`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("manifest.json lacks %s:\n%s", want, got)
		}
	}
	if strings.Contains(got, "widgets_charge") {
		t.Errorf("a provider job produced a job_types entry:\n%s", got)
	}

	if _, err := Generate(ctx, dir, GenerateOptions{Check: true}); err != nil {
		t.Errorf("--check on fresh output: %v", err)
	}

	changed := jobsFixtureSource("jobs.QueueBulk", "jobs.QueueEmail", "600*time.Second", "900*time.Second")
	if err := os.WriteFile(filepath.Join(dir, "cmd", "module", "main.go"), []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(ctx, dir, GenerateOptions{Check: true}); err == nil || !strings.Contains(err.Error(), "job_types") {
		t.Fatalf("--check after changing the queue and timeout = %v, want it to name job_types", err)
	}
	if _, err := Generate(ctx, dir, GenerateOptions{}); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if got := readFile(t, filepath.Join(dir, "manifest.json")); !strings.Contains(got, `"queue": "email"`) || !strings.Contains(got, `"timeout_seconds": 900`) {
		t.Errorf("manifest.json did not follow the changed definition:\n%s", got)
	}
}

func TestGenerate_JobCrossReferenceFailuresFailGeneration(t *testing.T) {
	tests := []struct {
		name         string
		replacements []string
		want         string
	}{
		{
			name:         "UniqueBy names a field the payload lacks",
			replacements: []string{`jobs.UniqueBy("file_id")`, `jobs.UniqueBy("nope")`},
			want:         `jobs.UniqueBy("nope") names no field of its payload type`,
		},
		{
			name:         "definition without a handler",
			replacements: []string{"engine.HandleJob(importJob, handleImport)", "_ = importJob"},
			want:         `"widgets_import" is declared with jobs.Define but has no engine.HandleJob registration`,
		},
		{
			name:         "cron without a handler",
			replacements: []string{"engine.HandleCron(sweep, handleSweep)", "_ = sweep"},
			want:         `"widgets_sweep" is declared with jobs.DefineCron but has no engine.HandleCron registration`,
		},
		{
			name:         "value outside the documented range",
			replacements: []string{"jobs.UniqueBy", "jobs.MaxAttempts(99), jobs.UniqueBy"},
			want:         "MaxAttempts 99 must be 1-25",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := writeCollectFixture(t, jobsFixtureSource(tt.replacements...), jobsFixtureManifest)

			_, err := Generate(generateCtx(t), dir, GenerateOptions{})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Generate = %v, want it to contain %q", err, tt.want)
			}
			if got := readFile(t, filepath.Join(dir, "manifest.json")); got != jobsFixtureManifest {
				t.Errorf("manifest.json was written:\n%s", got)
			}
		})
	}
}
