package adminmodules

import (
	"encoding/json/v2"
	"errors"
	"net/http"
	"slices"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/cronsettings"
	"github.com/djangbahevans/goerp/internal/engine/cronspec"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/mfa/enforce"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/route"
)

type cronJobJSON struct {
	Name             string     `json:"name"`
	Label            string     `json:"label"`
	Description      string     `json:"description"`
	Schedule         string     `json:"schedule"`
	Queue            string     `json:"queue"`
	TimeoutSeconds   int        `json:"timeout_seconds"`
	EnabledByDefault bool       `json:"enabled_by_default"`
	Enabled          bool       `json:"enabled"`
	Generation       string     `json:"generation"`
	EffectiveEnabled bool       `json:"effective_enabled"`
	BlockedReason    *string    `json:"blocked_reason"`
	NextRunAt        *time.Time `json:"next_run_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

func cronItem(m *module.LoadedModule, c caller, job manifest.CronJob, state cronsettings.State, now time.Time) cronJobJSON {
	item := cronJobJSON{
		Name:             job.Name,
		Label:            job.Label,
		Description:      job.Description,
		Schedule:         job.Schedule,
		Queue:            job.EffectiveQueue(),
		TimeoutSeconds:   job.EffectiveTimeoutSeconds(),
		EnabledByDefault: job.IsEnabledByDefault(),
		Enabled:          state.Enabled,
		Generation:       state.Generation,
		UpdatedAt:        state.UpdatedAt.UTC(),
	}
	switch {
	case !c.entitlements.ModuleEntitled(m.Manifest.Name):
		item.BlockedReason = new("module_not_entitled")
	case m.Status != module.StatusReady:
		item.BlockedReason = new("module_not_ready")
	case !c.entitlements.ModuleEnabled(m.Manifest.Name):
		item.BlockedReason = new("module_disabled")
	case !state.Enabled:
		item.BlockedReason = new("job_disabled")
	default:
		item.EffectiveEnabled = true
		if schedule, err := cronspec.Parse(job.Schedule); err == nil {
			if next := schedule.Next(now.UTC()); !next.IsZero() {
				item.NextRunAt = new(next)
			}
		}
	}
	return item
}

func (h *Handler) cronModule(w http.ResponseWriter, r *http.Request, c *caller) (*module.LoadedModule, bool) {
	snap := h.Registry.Snapshot()
	if snap == nil {
		writeError(w, http.StatusServiceUnavailable, "module_not_ready", "module registry is unavailable")
		return nil, false
	}
	m, found := snap.Modules()[route.ParamsFromContext(r.Context())["name"]]
	if !found || m.Manifest.Name == "" {
		writeError(w, http.StatusNotFound, "not_found", "module is not installed")
		return nil, false
	}
	ents, err := h.Tenants.LoadCurrentEntitlements(r.Context(), c.tenantID)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "cron_settings_unavailable", "couldn't read scheduled job eligibility")
		return nil, false
	}
	c.entitlements = ents
	return m, true
}

func (h *Handler) authorizeCron(w http.ResponseWriter, r *http.Request) (caller, bool) {
	c, ok := h.authorize(w, r)
	if !ok {
		return caller{}, false
	}
	if c.auth.AuthMethod != "jwt" {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "a tenant session is required")
		return caller{}, false
	}
	decision, err := h.Auth.EnforceMFA(r.Context(), r.URL.Path, c.tenantID, c.auth)
	if err != nil {
		internalError(w, c, "enforce cron session MFA", err)
		return caller{}, false
	}
	if decision != enforce.Allowed {
		writeError(w, http.StatusForbidden, string(decision), "mfa enforcement required")
		return caller{}, false
	}
	return c, true
}

func (h *Handler) ServeCronList(w http.ResponseWriter, r *http.Request) {
	c, ok := h.authorizeCron(w, r)
	if !ok {
		return
	}
	m, ok := h.cronModule(w, r, &c)
	if !ok {
		return
	}
	if h.Cron == nil {
		writeError(w, http.StatusServiceUnavailable, "cron_settings_unavailable", "scheduled job settings are unavailable")
		return
	}

	identities := make([]cronsettings.Identity, len(m.Manifest.CronJobs))
	for i, job := range m.Manifest.CronJobs {
		identities[i] = cronsettings.Identity{Module: m.Manifest.Name, Name: job.Name}
	}
	states, err := h.Cron.Read(r.Context(), c.tenantSlug, identities)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "cron_settings_unavailable", "scheduled job settings are unavailable")
		return
	}

	jobs := make([]cronJobJSON, len(identities))
	now := time.Now().UTC()
	for i, job := range m.Manifest.CronJobs {
		jobs[i] = cronItem(m, c, job, states[identities[i]], now)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"module":         m.Manifest.Name,
		"entitled":       c.entitlements.ModuleEntitled(m.Manifest.Name),
		"module_enabled": c.entitlements.ModuleEnabled(m.Manifest.Name),
		"module_ready":   m.Status == module.StatusReady,
		"cron_jobs":      jobs,
	})
}

func (h *Handler) ServeCronPatch(w http.ResponseWriter, r *http.Request) {
	c, ok := h.authorizeCron(w, r)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	var body struct {
		Enabled            *bool   `json:"enabled"`
		ExpectedGeneration *string `json:"expected_generation"`
	}
	if err := json.UnmarshalRead(r.Body, &body, json.RejectUnknownMembers(true)); err != nil || body.Enabled == nil || body.ExpectedGeneration == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "enabled and expected_generation are required")
		return
	}
	parsed, err := uuid.Parse(*body.ExpectedGeneration)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "expected_generation must be a UUID")
		return
	}

	m, ok := h.cronModule(w, r, &c)
	if !ok {
		return
	}
	name := route.ParamsFromContext(r.Context())["cron_name"]
	i := slices.IndexFunc(m.Manifest.CronJobs, func(job manifest.CronJob) bool { return job.Name == name })
	if i < 0 {
		writeError(w, http.StatusNotFound, "not_found", "scheduled job is not declared")
		return
	}
	if !c.entitlements.ModuleEntitled(m.Manifest.Name) {
		writeError(w, http.StatusConflict, "module_not_entitled", "your plan does not include this module")
		return
	}
	if m.Status != module.StatusReady {
		writeError(w, http.StatusConflict, "module_not_ready", "this module is unavailable")
		return
	}
	if h.Cron == nil {
		writeError(w, http.StatusServiceUnavailable, "cron_settings_unavailable", "scheduled job settings are unavailable")
		return
	}

	state, err := h.Cron.SetEnabled(r.Context(), c.tenantID, c.tenantSlug,
		cronsettings.Identity{Module: m.Manifest.Name, Name: name}, *body.Enabled, parsed.String(), c.userID)
	if err != nil {
		if errors.Is(err, cronsettings.ErrConflict) {
			writeError(w, http.StatusConflict, "cron_settings_conflict", "this scheduled job changed in another session")
		} else {
			writeError(w, http.StatusServiceUnavailable, "cron_settings_unavailable", "couldn't save scheduled job settings")
		}
		return
	}

	// A confirmed commit is acknowledged using request metadata; fresh reads can fail after the write.
	writeJSON(w, http.StatusOK, cronItem(m, c, m.Manifest.CronJobs[i], state, time.Now().UTC()))
}
