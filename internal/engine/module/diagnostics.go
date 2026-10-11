package module

import (
	"slices"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
)

func (m *LoadedModule) Warn(rule, message string, context map[string]string) {
	diagnostic := manifest.Warning(m.Manifest.Name, rule, message, context)
	m.Diagnostics = append(m.Diagnostics, diagnostic)
	diagnostic.Log()
}

func (m *LoadedModule) ClearDiagnostics(rule string) {
	m.Diagnostics = slices.DeleteFunc(m.Diagnostics, func(d manifest.Diagnostic) bool { return d.Rule == rule })
}
