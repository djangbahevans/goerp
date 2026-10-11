package manifest

import (
	"net/url"
	"slices"
	"strconv"
	"unicode/utf8"

	"github.com/rs/zerolog/log"
)

const (
	RuleUnknownRole            = "default_roles.unknown"
	RuleMissingSeed            = "config_schema.missing_seed"
	RuleLongDescription        = "long_description.length"
	RuleInsecureHTTP           = "http_allowlist.insecure"
	RuleMissingEmitter         = "subscribes.missing_emitter"
	RuleMissingSection         = "view_extensions.missing_section"
	RuleMissingPermissionOwner = "uses_permissions.missing_owner"
)

type Diagnostic struct {
	Rule     string            `json:"rule"`
	Severity string            `json:"severity"`
	Module   string            `json:"module"`
	Message  string            `json:"message"`
	Context  map[string]string `json:"context,omitempty"`
}

func Warning(module, rule, message string, context map[string]string) Diagnostic {
	return Diagnostic{
		Rule:     rule,
		Severity: "warning",
		Module:   module,
		Message:  message,
		Context:  context,
	}
}

func (d Diagnostic) Log() {
	event := log.Warn().Str("module", d.Module).Str("rule", d.Rule).Str("severity", d.Severity)

	for key, value := range d.Context {
		event.Str(key, value)
	}

	event.Msg(d.Message)
}

func manifestWarnings(m Manifest) []Diagnostic {
	var diagnostics []Diagnostic

	for _, p := range m.Permissions {
		for _, role := range p.DefaultRoles {
			if !slices.Contains(validDefaultRoles, role) {
				diagnostics = append(diagnostics, Warning(m.Name, RuleUnknownRole, "default_roles references an unknown role name", map[string]string{"permission": p.Name, "role": role}))
			}
		}
	}

	for _, config := range m.ConfigSchema {
		key := m.Name + "." + config.Key
		if _, seeded := m.TenantConfigSeeds[key]; !seeded {
			diagnostics = append(diagnostics, Warning(m.Name, RuleMissingSeed, "config_schema key has no tenant_config_seeds value", map[string]string{"key": key}))
		}
	}

	if length := utf8.RuneCountInString(m.LongDescription); length > 4096 {
		diagnostics = append(diagnostics, Warning(m.Name, RuleLongDescription, "long_description exceeds 4096 characters", map[string]string{"length": strconv.Itoa(length)}))
	}

	for _, entry := range m.HTTPAllowlist {
		if u, err := url.Parse(entry); err == nil && u.Scheme == "http" {
			diagnostics = append(diagnostics, Warning(m.Name, RuleInsecureHTTP, "http_allowlist declares an insecure outbound URL", map[string]string{"url": entry}))
		}
	}

	return diagnostics
}
