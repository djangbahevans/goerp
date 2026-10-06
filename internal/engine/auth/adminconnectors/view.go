package adminconnectors

import (
	"cmp"
	"context"
	"encoding/json/jsontext"
	"net/http"
	"slices"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/providerselect"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/djangbahevans/goerp/internal/engine/tenantconfig"
	"github.com/djangbahevans/goerp/internal/engine/tenantschema"
)

// providerInfo is a connector's standing in its single-active provider
// category (connector-guide.md §7). Primary is whether it is the tenant's
// active provider; CanSetPrimary is whether another enabled provider exists,
// so choosing is meaningful.
type providerInfo struct {
	Category      string `json:"category"`
	Primary       bool   `json:"primary"`
	CanSetPrimary bool   `json:"can_set_primary"`
}

type connectorSummary struct {
	Name           string        `json:"name"`
	DisplayName    string        `json:"display_name"`
	Description    string        `json:"description,omitempty"`
	Version        string        `json:"version"`
	Enabled        bool          `json:"enabled"`
	Configured     bool          `json:"configured"`
	HasStatusRoute bool          `json:"has_status_route"`
	Provider       *providerInfo `json:"provider"`
}

// configEntryView is one config_schema entry with the tenant's current value.
// Value is the stored value, else the default; for an encrypted entry it is
// "***" when set and null otherwise, never the plaintext.
type configEntryView struct {
	Key             string                 `json:"key"`
	Label           string                 `json:"label"`
	Description     string                 `json:"description,omitempty"`
	Type            string                 `json:"type"`
	FieldType       string                 `json:"field_type,omitempty"`
	Category        string                 `json:"category,omitempty"`
	Required        bool                   `json:"required"`
	Options         []manifest.FieldOption `json:"options,omitempty"`
	Min             any                    `json:"min,omitempty"`
	Max             any                    `json:"max,omitempty"`
	ValidationRegex string                 `json:"validation_regex,omitempty"`
	Encrypted       bool                   `json:"encrypted"`
	Generated       bool                   `json:"generated"`
	RestartRequired bool                   `json:"restart_required"`
	Default         any                    `json:"default"`
	IsSet           bool                   `json:"is_set"`
	Value           any                    `json:"value"`
}

type connectorDetail struct {
	connectorSummary
	Config []configEntryView `json:"config"`
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

func (h *Handler) provider(ctx context.Context, tenantID string, m *module.LoadedModule) (*providerInfo, error) {
	categories := make([]string, 0, len(m.Manifest.Provides))
	for category, provided := range m.Manifest.Provides {
		if provided && providerselect.IsCategory(category) && !providerselect.IsMultiActive(category) {
			categories = append(categories, category)
		}
	}
	if len(categories) == 0 {
		return nil, nil
	}
	slices.Sort(categories)
	category := categories[0]

	enabled, err := h.Providers.EnabledProviders(ctx, tenantID, category)
	if err != nil {
		return nil, err
	}
	info := &providerInfo{Category: category, CanSetPrimary: len(enabled) > 1}
	if primary, err := h.Providers.Resolve(ctx, tenantID, category); err == nil {
		info.Primary = primary == m.Manifest.Name
	}
	return info, nil
}

func (h *Handler) summary(ctx context.Context, c caller, snap interface{ RouteTable() *route.RouteTable }, m *module.LoadedModule, disabled []string) (connectorSummary, map[string]tenantconfig.ModuleConfigRow, error) {
	name := m.Manifest.Name
	rows, err := h.Config.ModuleConfigRows(ctx, tenantschema.Name(c.tenantSlug), name)
	if err != nil {
		return connectorSummary{}, nil, err
	}
	provider, err := h.provider(ctx, c.tenantID, m)
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
		Provider:       provider,
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

	entries := make([]configEntryView, 0, len(m.Manifest.ConfigSchema))
	for _, e := range m.Manifest.ConfigSchema {
		entries = append(entries, entryView(e, rows))
	}
	writeJSON(w, http.StatusOK, connectorDetail{connectorSummary: s, Config: entries})
}

func entryView(e manifest.ConfigEntry, rows map[string]tenantconfig.ModuleConfigRow) configEntryView {
	row, set := rows[e.Key]
	v := configEntryView{
		Key: e.Key, Label: e.Label, Description: e.Description, Type: e.Type, FieldType: e.FieldType,
		Category: e.Category, Required: e.Required, Options: e.Options, Min: e.Min, Max: e.Max,
		ValidationRegex: e.ValidationRegex, Encrypted: e.Encrypted, Generated: e.Generated,
		RestartRequired: e.RestartRequired, Default: e.Default, IsSet: set,
	}
	switch {
	case e.Encrypted:
		if set {
			v.Value = maskedValue
		}
	case set:
		v.Value = jsontext.Value(row.Value)
	default:
		v.Value = e.Default
	}
	return v
}
