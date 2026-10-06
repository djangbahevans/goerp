package connectoringress

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
)

const localPostgresDSN = "postgres://goerp:dev@localhost:15432/goerp"

type testEnv struct {
	store       *Store
	tenantStore *tenant.Store
	conn        *sql.DB
}

func openTestStore(t *testing.T) *testEnv {
	t.Helper()

	conn, err := db.New(localPostgresDSN)
	if err != nil {
		t.Skipf("postgres not reachable at %s (start compose.dev.yml): %v", localPostgresDSN, err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	tenantStore := tenant.NewStore(conn)
	if err := tenantStore.Bootstrap(t.Context()); err != nil {
		t.Fatalf("tenant Bootstrap() error: %v", err)
	}
	store := NewStore(conn)
	if err := store.Bootstrap(t.Context()); err != nil {
		t.Fatalf("Bootstrap() error: %v", err)
	}
	return &testEnv{store: store, tenantStore: tenantStore, conn: conn}
}

// createTenant creates a tenant and deletes it on cleanup; endpoint and
// inbox rows cascade with it.
func (e *testEnv) createTenant(t *testing.T) string {
	t.Helper()
	slug := fmt.Sprintf("ingresstest%d", time.Now().UnixNano())
	tt, err := e.tenantStore.CreateTenant(t.Context(), slug, "Ingress Test Co")
	if err != nil {
		t.Fatalf("CreateTenant(%q) error: %v", slug, err)
	}
	t.Cleanup(func() { _, _ = e.conn.Exec("DELETE FROM system.tenants WHERE id = $1", tt.ID) })
	return tt.ID
}

func TestEncodeToken_PadsToFixedLength(t *testing.T) {
	zero := make([]byte, tokenBytes)
	if got, want := encodeToken(zero), strings.Repeat("0", tokenLength); got != want {
		t.Errorf("encodeToken(zero) = %q, want %q", got, want)
	}

	max := make([]byte, tokenBytes)
	for i := range max {
		max[i] = 0xff
	}
	if got := encodeToken(max); len(got) != tokenLength {
		t.Errorf("len(encodeToken(max)) = %d, want %d", len(got), tokenLength)
	}
}

func TestNewToken_IsBase62AndUnique(t *testing.T) {
	base62 := regexp.MustCompile(`^[0-9A-Za-z]{43}$`)
	seen := map[string]bool{}
	for range 200 {
		token, err := newToken()
		if err != nil {
			t.Fatalf("newToken() error: %v", err)
		}
		if !base62.MatchString(token) {
			t.Fatalf("token %q is not 43 base62 characters", token)
		}
		if seen[token] {
			t.Fatalf("token %q generated twice", token)
		}
		seen[token] = true
	}
}

func TestBootstrap_IsIdempotent(t *testing.T) {
	env := openTestStore(t)

	if err := env.store.Bootstrap(t.Context()); err != nil {
		t.Fatalf("second Bootstrap() error: %v", err)
	}
}

func TestMintEndpoint_ReturnsSameTokenWhileActive(t *testing.T) {
	env := openTestStore(t)
	tenantID := env.createTenant(t)

	first, err := env.store.MintEndpoint(t.Context(), tenantID, "connector_paystack")
	if err != nil {
		t.Fatalf("MintEndpoint() error: %v", err)
	}
	second, err := env.store.MintEndpoint(t.Context(), tenantID, "connector_paystack")
	if err != nil {
		t.Fatalf("second MintEndpoint() error: %v", err)
	}
	if first != second {
		t.Errorf("second mint = %q, want the active token %q", second, first)
	}

	other, err := env.store.MintEndpoint(t.Context(), tenantID, "connector_stripe")
	if err != nil {
		t.Fatalf("MintEndpoint(other module) error: %v", err)
	}
	if other == first {
		t.Error("two modules of one tenant share a token")
	}
}

func TestMintEndpoint_ConcurrentCallersShareOneToken(t *testing.T) {
	env := openTestStore(t)
	tenantID := env.createTenant(t)

	const callers = 12
	tokens := make([]string, callers)
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Go(func() {
			tokens[i], errs[i] = env.store.MintEndpoint(t.Context(), tenantID, "connector_paystack")
		})
	}
	wg.Wait()

	for i := range callers {
		if errs[i] != nil {
			t.Fatalf("caller %d: %v", i, errs[i])
		}
		if tokens[i] != tokens[0] {
			t.Errorf("caller %d got %q, caller 0 got %q", i, tokens[i], tokens[0])
		}
	}
}

func TestMintEndpoint_AfterRevokeIssuesNewToken(t *testing.T) {
	env := openTestStore(t)
	tenantID := env.createTenant(t)

	old, err := env.store.MintEndpoint(t.Context(), tenantID, "connector_paystack")
	if err != nil {
		t.Fatalf("MintEndpoint() error: %v", err)
	}
	if err := env.store.RevokeEndpoint(t.Context(), tenantID, "connector_paystack"); err != nil {
		t.Fatalf("RevokeEndpoint() error: %v", err)
	}

	fresh, err := env.store.MintEndpoint(t.Context(), tenantID, "connector_paystack")
	if err != nil {
		t.Fatalf("MintEndpoint() after revoke error: %v", err)
	}
	if fresh == old {
		t.Error("mint after revoke reused the revoked token")
	}
	if _, err := env.store.ResolveEndpoint(t.Context(), old, "connector_paystack"); !errors.Is(err, ErrEndpointNotFound) {
		t.Errorf("revoked token resolves: err = %v, want ErrEndpointNotFound", err)
	}
	got, err := env.store.ResolveEndpoint(t.Context(), fresh, "connector_paystack")
	if err != nil || got != tenantID {
		t.Errorf("ResolveEndpoint(fresh) = %q, %v; want %q", got, err, tenantID)
	}
}

func TestResolveEndpoint(t *testing.T) {
	env := openTestStore(t)
	tenantID := env.createTenant(t)
	token, err := env.store.MintEndpoint(t.Context(), tenantID, "connector_paystack")
	if err != nil {
		t.Fatalf("MintEndpoint() error: %v", err)
	}

	tests := []struct {
		name   string
		token  string
		module string
		want   string
		found  bool
	}{
		{"active token and module", token, "connector_paystack", tenantID, true},
		{"token under another module", token, "connector_stripe", "", false},
		{"unknown token", strings.Repeat("0", tokenLength), "connector_paystack", "", false},
		{"empty token", "", "connector_paystack", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := env.store.ResolveEndpoint(t.Context(), tt.token, tt.module)
			if tt.found {
				if err != nil || got != tt.want {
					t.Errorf("ResolveEndpoint() = %q, %v; want %q", got, err, tt.want)
				}
				return
			}
			if !errors.Is(err, ErrEndpointNotFound) {
				t.Errorf("ResolveEndpoint() error = %v, want ErrEndpointNotFound", err)
			}
		})
	}
}

func TestRevokeEndpoint_WithoutActiveEndpoint(t *testing.T) {
	env := openTestStore(t)
	tenantID := env.createTenant(t)

	if err := env.store.RevokeEndpoint(t.Context(), tenantID, "connector_paystack"); !errors.Is(err, ErrEndpointNotFound) {
		t.Errorf("revoke with no endpoint: err = %v, want ErrEndpointNotFound", err)
	}

	if _, err := env.store.MintEndpoint(t.Context(), tenantID, "connector_paystack"); err != nil {
		t.Fatalf("MintEndpoint() error: %v", err)
	}
	if err := env.store.RevokeEndpoint(t.Context(), tenantID, "connector_paystack"); err != nil {
		t.Fatalf("RevokeEndpoint() error: %v", err)
	}
	if err := env.store.RevokeEndpoint(t.Context(), tenantID, "connector_paystack"); !errors.Is(err, ErrEndpointNotFound) {
		t.Errorf("second revoke: err = %v, want ErrEndpointNotFound", err)
	}
}

func TestInsertInbox_DuplicateEventIsNotInserted(t *testing.T) {
	env := openTestStore(t)
	tenantID := env.createTenant(t)
	otherTenantID := env.createTenant(t)
	payload := []byte(`{"reference":"ref_1"}`)

	id, inserted, err := env.store.InsertInbox(t.Context(), tenantID, "connector_paystack", "evt_1", payload)
	if err != nil || !inserted || id == "" {
		t.Fatalf("first InsertInbox() = %q, %v, %v; want a new row", id, inserted, err)
	}

	dupID, dupInserted, err := env.store.InsertInbox(t.Context(), tenantID, "connector_paystack", "evt_1", []byte(`{"reference":"changed"}`))
	if err != nil || dupInserted || dupID != "" {
		t.Errorf("duplicate InsertInbox() = %q, %v, %v; want no row and no error", dupID, dupInserted, err)
	}

	var stored, status string
	err = env.conn.QueryRowContext(t.Context(),
		`SELECT payload::text, status FROM system.connector_inbox WHERE id = $1`, id).Scan(&stored, &status)
	if err != nil {
		t.Fatalf("read inbox row: %v", err)
	}
	if !strings.Contains(stored, "ref_1") || status != "pending" {
		t.Errorf("stored row = %q status %q, want the first payload and pending", stored, status)
	}

	for _, tt := range []struct{ name, tenant, module, event string }{
		{"another module", tenantID, "connector_stripe", "evt_1"},
		{"another tenant", otherTenantID, "connector_paystack", "evt_1"},
		{"another event", tenantID, "connector_paystack", "evt_2"},
	} {
		if _, inserted, err := env.store.InsertInbox(t.Context(), tt.tenant, tt.module, tt.event, payload); err != nil || !inserted {
			t.Errorf("%s: InsertInbox() = %v, %v; want a new row", tt.name, inserted, err)
		}
	}
}

func TestInsertInbox_ConcurrentDuplicatesInsertOnce(t *testing.T) {
	env := openTestStore(t)
	tenantID := env.createTenant(t)

	const deliveries = 16
	var mu sync.Mutex
	var insertedCount int
	var wg sync.WaitGroup
	for range deliveries {
		wg.Go(func() {
			_, inserted, err := env.store.InsertInbox(t.Context(), tenantID, "connector_paystack", "evt_race", []byte(`{}`))
			if err != nil {
				t.Errorf("InsertInbox() error: %v", err)
				return
			}
			if inserted {
				mu.Lock()
				insertedCount++
				mu.Unlock()
			}
		})
	}
	wg.Wait()

	if insertedCount != 1 {
		t.Errorf("%d deliveries of one event inserted %d rows, want 1", deliveries, insertedCount)
	}
}

func TestInsertInbox_RejectsInvalidJSON(t *testing.T) {
	env := openTestStore(t)
	tenantID := env.createTenant(t)

	if _, _, err := env.store.InsertInbox(t.Context(), tenantID, "connector_paystack", "evt_bad", []byte(`not json`)); err == nil {
		t.Error("InsertInbox() accepted a payload that is not JSON")
	}
}

func TestDeletingTenantCascadesToIngressRows(t *testing.T) {
	env := openTestStore(t)
	tenantID := env.createTenant(t)
	if _, err := env.store.MintEndpoint(t.Context(), tenantID, "connector_paystack"); err != nil {
		t.Fatalf("MintEndpoint() error: %v", err)
	}
	if _, _, err := env.store.InsertInbox(t.Context(), tenantID, "connector_paystack", "evt_1", []byte(`{}`)); err != nil {
		t.Fatalf("InsertInbox() error: %v", err)
	}

	if _, err := env.conn.ExecContext(t.Context(), `DELETE FROM system.tenants WHERE id = $1`, tenantID); err != nil {
		t.Fatalf("delete tenant: %v", err)
	}

	for _, table := range []string{"connector_webhook_endpoints", "connector_inbox"} {
		var remaining int
		query := fmt.Sprintf(`SELECT count(*) FROM system.%s WHERE tenant_id = $1`, table)
		if err := env.conn.QueryRowContext(t.Context(), query, tenantID).Scan(&remaining); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if remaining != 0 {
			t.Errorf("%s keeps %d rows of a deleted tenant", table, remaining)
		}
	}
}
