package wasm

import (
	"context"
	"fmt"
	"testing"
	"uuid"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
	"github.com/vmihailenco/msgpack/v5"
)

func newCompanyConfigCaller(t *testing.T, r *Runtime, tenantID string) *ModuleInstance {
	t.Helper()

	compiled, err := r.wazero.CompileModule(t.Context(), buildHostCallerModule("host.config", []string{"get", "set"}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = compiled.Close(context.Background()) })

	inst, err := newModuleInstance(t.Context(), "company-config-"+uuid.New().String(), compiled, r)
	if err != nil {
		t.Fatal(err)
	}

	inst.SetModuleContext(NewModuleContext("req", "reports", "user", "", nil, nil, tenantID, "", "trace", 0, nil, ModuleSnapshot{}))
	r.RegisterInstance(inst)
	t.Cleanup(func() { r.UnregisterInstance(inst) })

	return inst
}

func TestCompanyConfig_ProfileReadsAreTenantScopedAndFresh(t *testing.T) {
	db := openTestPrimaryDB(t)
	profiles := tenant.NewStore(db)
	if err := profiles.Bootstrap(t.Context()); err != nil {
		t.Fatal(err)
	}

	r := newHostcallTestRuntime(t, db, 10)
	r.SetCompanyProfileStore(profiles)

	read := func(inst *ModuleInstance, key string, want any, found bool) {
		t.Helper()

		env := callHost(t, t.Context(), inst, "call_get", abiv1.ConfigGetInput{Key: key})
		if !env.OK {
			t.Fatalf("get %s: %+v", key, env.Error)
		}

		var got abiv1.ConfigGetOutput
		if err := msgpack.Unmarshal(env.Data, &got); err != nil {
			t.Fatal(err)
		}
		if got.Value != want || got.Found != found {
			t.Fatalf("get %s = %+v, want value=%v found=%v", key, got, want, found)
		}
	}

	for i := range 2 {
		name := fmt.Sprintf("Company %d", i)
		ft, err := profiles.CreateTenant(t.Context(), "company"+uuid.New().String()[:8], name)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), "DELETE FROM system.tenants WHERE id = $1", ft.ID) })

		inst := newCompanyConfigCaller(t, r, ft.ID)
		read(inst, "company.name", name, true)
		for _, key := range []string{"company.address", "company.tax_id", "company.logo_url"} {
			read(inst, key, nil, false)
		}

		address := fmt.Sprintf("Address %d", i)
		taxID := fmt.Sprintf("TIN-%d", i)
		if _, err := profiles.UpdateProfile(t.Context(), ft.ID, tenant.ProfileUpdate{
			Name:    new(name + " Ltd"),
			Address: new(address),
			TaxID:   new(taxID),
		}); err != nil {
			t.Fatal(err)
		}

		read(inst, "company.name", name+" Ltd", true)
		read(inst, "company.address", address, true)
		read(inst, "company.tax_id", taxID, true)

		for _, logo := range []string{"https://cdn.example/first.png", "https://cdn.example/replacement.png", ""} {
			if _, err := profiles.SetLogoURL(t.Context(), ft.ID, logo); err != nil {
				t.Fatal(err)
			}
			if logo == "" {
				read(inst, "company.logo_url", nil, false)
			} else {
				read(inst, "company.logo_url", logo, true)
			}
		}

		if _, err := profiles.UpdateProfile(t.Context(), ft.ID, tenant.ProfileUpdate{Address: new(""), TaxID: new("")}); err != nil {
			t.Fatal(err)
		}
		read(inst, "company.address", nil, false)
		read(inst, "company.tax_id", nil, false)

		for _, key := range []string{"company.name", "company.address", "company.tax_id", "company.logo_url", "company.unknown"} {
			env := callHost(t, t.Context(), inst, "call_set", abiv1.ConfigSetInput{Key: key, Value: "changed"})
			if env.OK || env.Error.Code != abiv1.ErrCodeConfigKeyUndeclared {
				t.Fatalf("set %s = %+v, want config.key_undeclared", key, env)
			}
		}
		read(inst, "company.name", name+" Ltd", true)

		env := callHost(t, t.Context(), inst, "call_get", abiv1.ConfigGetInput{Key: "company.unknown"})
		if env.OK || env.Error.Code != abiv1.ErrCodeConfigKeyUndeclared {
			t.Fatalf("unknown key = %+v, want config.key_undeclared", env)
		}
	}
}

func TestCompanyConfig_UnavailableStore(t *testing.T) {
	r := newHostcallTestRuntime(t, nil, 10)
	inst := newCompanyConfigCaller(t, r, uuid.New().String())

	env := callHost(t, t.Context(), inst, "call_get", abiv1.ConfigGetInput{Key: "company.name"})
	if env.OK || env.Error.Code != abiv1.ErrCodeUnavailable {
		t.Fatalf("missing store = %+v, want abi.unavailable", env)
	}
}
