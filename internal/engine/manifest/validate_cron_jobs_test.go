package manifest

import (
	"encoding/json/v2"
	"strings"
	"testing"
)

func manifestWithCronJobs(t *testing.T, jobs []map[string]any) []byte {
	t.Helper()
	fields := minimalManifestFields()
	fields["cron_jobs"] = jobs
	m, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return m
}

func validCronJob() map[string]any {
	return map[string]any{
		"name":     "weekly_dedupe_scan",
		"label":    "Weekly Duplicate Scan",
		"schedule": "0 3 * * 0",
		"handler":  "schedule_dedupe_scan",
	}
}

func cronJobWith(overrides map[string]any) map[string]any {
	job := validCronJob()
	for k, v := range overrides {
		if v == nil {
			delete(job, k)
			continue
		}
		job[k] = v
	}
	return job
}

func TestLoadManifest_ValidCronJobs_Pass(t *testing.T) {
	full := cronJobWith(map[string]any{
		"name": "nightly", "description": "Runs nightly", "enabled_by_default": false,
		"timeout_seconds": 86400, "queue": "email", "schedule": "30 8 * * mon-fri",
	})
	if _, err := Load(manifestWithCronJobs(t, []map[string]any{validCronJob(), full})); err != nil {
		t.Fatalf("expected well-formed cron_jobs to load, got %v", err)
	}
}

func TestLoadManifest_InvalidCronJobs_Rejected(t *testing.T) {
	tests := []struct {
		name string
		jobs []map[string]any
		want string
	}{
		{"missing name", []map[string]any{cronJobWith(map[string]any{"name": nil})}, "cron_jobs[0]: name is required"},
		{"missing label", []map[string]any{cronJobWith(map[string]any{"label": nil})}, `cron job "weekly_dedupe_scan": label is required`},
		{"missing schedule", []map[string]any{cronJobWith(map[string]any{"schedule": nil})}, `cron job "weekly_dedupe_scan": schedule is required`},
		{"missing handler", []map[string]any{cronJobWith(map[string]any{"handler": nil})}, `cron job "weekly_dedupe_scan": handler is required`},
		{"duplicate name", []map[string]any{validCronJob(), validCronJob()}, `cron job "weekly_dedupe_scan": name must be unique`},
		{"descriptor schedule", []map[string]any{cronJobWith(map[string]any{"schedule": "@daily"})}, `cron job "weekly_dedupe_scan": schedule "@daily" must have 5 fields`},
		{"seconds field", []map[string]any{cronJobWith(map[string]any{"schedule": "0 0 3 * * *"})}, `cron job "weekly_dedupe_scan": schedule "0 0 3 * * *" must have 5 fields`},
		{"out of range schedule", []map[string]any{cronJobWith(map[string]any{"schedule": "61 * * * *"})}, `cron job "weekly_dedupe_scan": schedule "61 * * * *"`},
		{"unknown queue", []map[string]any{cronJobWith(map[string]any{"queue": "urgent"})}, `cron job "weekly_dedupe_scan": queue "urgent" must be one of`},
		{"timeout over a day", []map[string]any{cronJobWith(map[string]any{"timeout_seconds": 86401})}, `cron job "weekly_dedupe_scan": timeout_seconds 86401 must be between 0 and 86400`},
		{"negative timeout", []map[string]any{cronJobWith(map[string]any{"timeout_seconds": -1})}, `cron job "weekly_dedupe_scan": timeout_seconds -1 must be between 0 and 86400`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load(manifestWithCronJobs(t, tt.jobs))
			if err == nil {
				t.Fatalf("expected rejection mentioning %q, got nil", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestCronJob_Defaults(t *testing.T) {
	m, err := Load(manifestWithCronJobs(t, []map[string]any{validCronJob()}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	job := m.CronJobs[0]
	if !job.IsEnabledByDefault() || job.EffectiveTimeoutSeconds() != 3600 || job.EffectiveQueue() != "bulk" {
		t.Errorf("defaults = enabled %v, timeout %d, queue %q; want true, 3600, bulk",
			job.IsEnabledByDefault(), job.EffectiveTimeoutSeconds(), job.EffectiveQueue())
	}
}

func TestCronJob_DeclaredValuesOverrideDefaults(t *testing.T) {
	m, err := Load(manifestWithCronJobs(t, []map[string]any{
		cronJobWith(map[string]any{"enabled_by_default": false, "timeout_seconds": 60, "queue": "search"}),
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	job := m.CronJobs[0]
	if job.IsEnabledByDefault() || job.EffectiveTimeoutSeconds() != 60 || job.EffectiveQueue() != "search" {
		t.Errorf("values = enabled %v, timeout %d, queue %q; want false, 60, search",
			job.IsEnabledByDefault(), job.EffectiveTimeoutSeconds(), job.EffectiveQueue())
	}
}
