package adminmodules

import (
	"encoding/json/v2"
	"net/http"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/registry"
)

func TestModuleQueriesExposeDiagnostics(t *testing.T) {
	e := newEnv(t)
	ft := e.newTenant(t)
	token := e.token(t, ft, "admin")
	reg := e.handler.Registry.(*registry.ModuleRegistry)
	modules := reg.Snapshot().Modules()
	m := *modules[modApp]
	m.Diagnostics = []manifest.Diagnostic{manifest.Warning(modApp, manifest.RuleMissingSeed, "config_schema key has no tenant_config_seeds value", map[string]string{"key": modApp + ".prefix"})}
	modules[modApp] = &m
	if _, err := reg.Update(modules); err != nil {
		t.Fatal(err)
	}

	list := e.list(t, ft, token)
	requireStatus(t, list, http.StatusOK, "")
	got := findModule(t, list, modApp)
	if len(got.Diagnostics) != 1 || got.Diagnostics[0].Rule != manifest.RuleMissingSeed {
		t.Fatalf("list diagnostics=%+v", got.Diagnostics)
	}

	detail := e.get(t, ft, token, modApp)
	requireStatus(t, detail, http.StatusOK, "")
	var response moduleDetailJSON
	if err := json.Unmarshal(detail.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Diagnostics) != 1 || response.Diagnostics[0].Module != modApp || response.Diagnostics[0].Context["key"] != modApp+".prefix" {
		t.Fatalf("detail diagnostics=%+v", response.Diagnostics)
	}
}
