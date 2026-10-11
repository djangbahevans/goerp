package tenantconfig

import "testing"

func TestResolverTypedManifestSeedsWithoutStoredRows(t *testing.T) {
	env := openTestEnv(t)
	tenant := env.createTenant(t)
	env.createModuleConfigSchema(t, tenant.Slug)

	tests := []struct {
		name  string
		value any
		want  string
	}{
		{name: "string", value: "GH", want: "GH"},
		{name: "integer", value: int64(9223372036854775807), want: "9223372036854775807"},
		{name: "float", value: 0.85, want: "0.85"},
		{name: "boolean", value: false, want: "false"},
		{name: "duration", value: "15m", want: "15m"},
		{name: "json", value: map[string]any{"enabled": true}, want: `{"enabled":true}`},
		{name: "null json", want: "null"},
		{name: "strings", value: []string{"GH", "KE"}, want: `["GH","KE"]`},
		{name: "integers", value: []int64{1, 2}, want: `[1,2]`},
		{name: "floats", value: []float64{1, 2.5}, want: `[1,2.5]`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolver := NewResolver(env.store, env.tenantStore, testRegistryWithSeed("demo", "setting", test.value))

			value, encrypted, found, err := resolver.Get(t.Context(), tenant.ID, "demo.setting")

			if err != nil || !found || encrypted || value != test.want {
				t.Fatalf("manifest fallback = (%q, encrypted %v, found %v, error %v), want %q", value, encrypted, found, err, test.want)
			}
		})
	}
}
