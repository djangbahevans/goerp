package modeltest

import (
	"crypto/rand"
	"database/sql"
	"encoding/json/v2"
	"slices"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/auth/rowcrypt"
	"github.com/djangbahevans/goerp/internal/engine/configvalue"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/internal/engine/tenantconfig"
	"github.com/djangbahevans/goerp/sdk/go/config"
)

// TestConfig holds the harness's tenant config fixtures. Use SetConfig to set typed values.
type TestConfig struct {
	store    *tenantconfig.Store
	resolver *tenantconfig.Resolver
	keys     *rowcrypt.RowKeySet
}

func newTestConfig(h *Harness, db *sql.DB) *TestConfig {
	h.t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		h.t.Fatalf("modeltest: generate config encryption key: %v", err)
	}
	c := &TestConfig{
		store: tenantconfig.NewStore(db),
		keys: &rowcrypt.RowKeySet{Active: rowcrypt.RowKey{
			KeyID: uuid.New().String(),
			Key:   key,
		}},
	}
	c.resolver = tenantconfig.NewResolver(c.store, h.tenants, h.registry)
	h.runtime.SetTenantConfig(c.resolver, c.store)
	h.runtime.SetCompanyProfileStore(h.tenants)
	h.runtime.SetRowCryptKeys(c.keys)

	return c
}

// WithConfig seeds a typed config value before the harness is returned.
// An undeclared key, mismatched type or invalid value fails the test.
func WithConfig[T any](handle config.Value[T], value T) Option {
	return func(c *harnessConfig) {
		c.configValues = append(c.configValues, func(h *Harness) { SetConfig(h, handle, value) })
	}
}

// SetConfig sets the harness tenant's owned config key for subsequent handler reads.
// An undeclared key, mismatched type or invalid value fails the test.
func SetConfig[T any](h *Harness, handle config.Value[T], value T) {
	h.t.Helper()
	entries := h.module.Manifest.ConfigSchema
	i := slices.IndexFunc(entries, func(entry manifest.ConfigEntry) bool { return entry.Key == handle.Key() })
	if i < 0 || entries[i].Type != handle.Type() {
		h.t.Fatalf("modeltest: config %q of type %q is not declared by module %q", handle.Key(), handle.Type(), h.moduleName)
		return
	}

	var raw any
	switch v := any(value).(type) {
	case time.Duration:
		if handle.Type() == "duration" {
			raw = v.String()
		} else {
			raw = int64(v)
		}
	case int:
		raw = int64(v)
	case []int:
		items := make([]any, len(v))
		for i, item := range v {
			items[i] = int64(item)
		}
		raw = items
	default:
		data, err := json.Marshal(value)
		if err != nil {
			h.t.Fatalf("modeltest: encode config %q: %v", handle.Key(), err)
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			h.t.Fatalf("modeltest: normalize config %q: %v", handle.Key(), err)
		}
	}
	data, err := configvalue.Encode(entries[i], raw, h.Config.keys)
	if err != nil {
		h.t.Fatalf("modeltest: validate config %q: %v", handle.Key(), err)
	}
	if err := h.Config.store.SetModuleConfig(h.t.Context(), h.TenantID, h.DB.schema(), h.moduleName, handle.Key(), data, handle.Type(), entries[i].Encrypted, h.UserID); err != nil {
		h.t.Fatalf("modeltest: set config %q: %v", handle.Key(), err)
	}
	h.Config.resolver.Invalidate(h.TenantID, h.moduleName+"."+handle.Key())
}
