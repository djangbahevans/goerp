package adminconnectors

import (
	"cmp"
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/auth/configview"
	"github.com/djangbahevans/goerp/internal/engine/connectoringress"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/providerselect"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/internal/engine/tenantconfig"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

type providerInfo struct {
	Category      string `json:"category"`
	Primary       bool   `json:"primary"`
	CanSetPrimary bool   `json:"can_set_primary"`
}

type connectorSummary struct {
	Name           string         `json:"name"`
	DisplayName    string         `json:"display_name"`
	Description    string         `json:"description,omitempty"`
	Version        string         `json:"version"`
	Enabled        bool           `json:"enabled"`
	Configured     bool           `json:"configured"`
	HasStatusRoute bool           `json:"has_status_route"`
	Providers      []providerInfo `json:"providers"`
}

type connectorDetail struct {
	connectorSummary
	// WebhookPath is the active inbound webhook endpoint path of a connector
	// that verifies webhooks, absent until a complete configuration save mints it.
	WebhookPath string             `json:"webhook_path,omitempty"`
	Config      []configview.Entry `json:"config"`
}

func connectorModule(snap interface {
	Modules() map[string]*module.LoadedModule
}, name string) (*module.LoadedModule, bool) {
	m, ok := snap.Modules()[name]
	if !ok || m.Manifest.Type != "connector" {
		return nil, false
	}
	switch m.Status {
	case module.StatusFailed, module.StatusUnloaded:
		return nil, false
	}
	return m, true
}

// Configured reports whether every required config entry has a stored value
// or a default, the condition under which a connector can run
// (manifest-spec.md §17 "required").
func Configured(entries []manifest.ConfigEntry, rows map[string]tenantconfig.ModuleConfigRow) bool {
	for _, e := range entries {
		if !e.Required {
			continue
		}
		if _, set := rows[e.Key]; !set && e.Default == nil {
			return false
		}
	}
	return true
}

func (h *Handler) providers(ctx context.Context, tenantID string, m *module.LoadedModule) ([]providerInfo, error) {
	infos := []providerInfo{}
	for _, category := range providerselect.Categories(m.Manifest.Provides) {
		if providerselect.IsMultiActive(category) {
			continue
		}

		enabled, err := h.Providers.EnabledProviders(ctx, tenantID, category)
		if err != nil {
			return nil, err
		}

		info := providerInfo{Category: category, CanSetPrimary: len(enabled) > 1 && slices.Contains(enabled, m.Manifest.Name)}
		primary, err := h.Providers.Resolve(ctx, tenantID, category)
		switch {
		case err == nil:
			info.Primary = primary == m.Manifest.Name
		case errors.Is(err, providerselect.ErrNoProviderSelected), errors.Is(err, providerselect.ErrNoProviderInstalled):
		default:
			return nil, err
		}

		infos = append(infos, info)
	}

	return infos, nil
}

func (h *Handler) summary(ctx context.Context, c caller, snap interface{ RouteTable() *route.RouteTable }, m *module.LoadedModule, disabled []string) (connectorSummary, map[string]tenantconfig.ModuleConfigRow, error) {
	name := m.Manifest.Name
	rows, err := h.Config.ModuleConfigRows(ctx, tenantschema.Name(c.tenantSlug), name)
	if err != nil {
		return connectorSummary{}, nil, err
	}
	providers, err := h.providers(ctx, c.tenantID, m)
	if err != nil {
		return connectorSummary{}, nil, err
	}
	_, _, statusRoute, _ := snap.RouteTable().Lookup(http.MethodGet, "/connectors/"+name+"/status")

	return connectorSummary{
		Name:           name,
		DisplayName:    cmp.Or(m.Manifest.DisplayName, name),
		Description:    m.Manifest.Description,
		Version:        m.Manifest.Version,
		Enabled:        !slices.Contains(disabled, name),
		Configured:     Configured(m.Manifest.ConfigSchema, rows),
		HasStatusRoute: statusRoute == route.RouteFound,
		Providers:      providers,
	}, rows, nil
}

// ServeList is GET /admin/connectors.
func (h *Handler) ServeList(w http.ResponseWriter, r *http.Request) {
	c, ok := h.authorize(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	snap := h.Registry.Snapshot()
	if snap == nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "engine has not finished starting")
		return
	}
	disabled, err := h.Modules.DisabledModulesForTenant(ctx, c.tenantID)
	if err != nil {
		internalError(w, c, "list disabled modules", err)
		return
	}

	connectors := []connectorSummary{}
	for name := range snap.Modules() {
		m, ok := connectorModule(snap, name)
		if !ok {
			continue
		}
		s, _, err := h.summary(ctx, c, snap, m, disabled)
		if err != nil {
			internalError(w, c, "summarize connector "+name, err)
			return
		}
		connectors = append(connectors, s)
	}
	slices.SortFunc(connectors, func(a, b connectorSummary) int {
		return cmp.Or(strings.Compare(strings.ToLower(a.DisplayName), strings.ToLower(b.DisplayName)), strings.Compare(a.Name, b.Name))
	})

	writeJSON(w, http.StatusOK, map[string]any{"connectors": connectors})
}

// ServeGet is GET /admin/connectors/{name}.
func (h *Handler) ServeGet(w http.ResponseWriter, r *http.Request) {
	c, ok := h.authorize(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	snap := h.Registry.Snapshot()
	if snap == nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "engine has not finished starting")
		return
	}
	m, ok := connectorModule(snap, route.ParamsFromContext(ctx)["name"])
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "connector is not installed")
		return
	}
	disabled, err := h.Modules.DisabledModulesForTenant(ctx, c.tenantID)
	if err != nil {
		internalError(w, c, "list disabled modules", err)
		return
	}
	s, rows, err := h.summary(ctx, c, snap, m, disabled)
	if err != nil {
		internalError(w, c, "summarize connector", err)
		return
	}

	detail := connectorDetail{connectorSummary: s, Config: configview.Entries(m.Manifest.ConfigSchema, rows)}
	if m.HasWebhookVerifier {
		token, err := h.Endpoints.ActiveEndpoint(ctx, c.tenantID, m.Manifest.Name)
		switch {
		case err == nil:
			detail.WebhookPath = webhookPath(m.Manifest.Name, token)
		case !errors.Is(err, connectoringress.ErrEndpointNotFound):
			internalError(w, c, "read webhook endpoint", err)
			return
		}
	}
	writeJSON(w, http.StatusOK, detail)
}
