package connectoringress

import (
	"context"
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

func TestActiveEndpoint(t *testing.T) {
	env := openTestStore(t)
	tenantID := env.createTenant(t)

	if _, err := env.store.ActiveEndpoint(t.Context(), tenantID, "connector_paystack"); !errors.Is(err, ErrEndpointNotFound) {
		t.Errorf("before minting: err = %v, want ErrEndpointNotFound", err)
	}
	minted, err := env.store.MintEndpoint(t.Context(), tenantID, "connector_paystack")
	if err != nil {
		t.Fatalf("MintEndpoint() error: %v", err)
	}
	if got, err := env.store.ActiveEndpoint(t.Context(), tenantID, "connector_paystack"); err != nil || got != minted {
		t.Errorf("ActiveEndpoint() = %q, %v, want %q", got, err, minted)
	}
	if err := env.store.RevokeEndpoint(t.Context(), tenantID, "connector_paystack"); err != nil {
		t.Fatalf("RevokeEndpoint() error: %v", err)
	}
	if _, err := env.store.ActiveEndpoint(t.Context(), tenantID, "connector_paystack"); !errors.Is(err, ErrEndpointNotFound) {
		t.Errorf("after revoking: err = %v, want ErrEndpointNotFound", err)
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

func TestAcceptDelivery_EnqueuesOnlyNewDeliveriesInTheSameTransaction(t *testing.T) {
	env := openTestStore(t)
	tenantID := env.createTenant(t)

	var enqueued []string
	enqueue := func(ctx context.Context, tx *sql.Tx, inboxID string) error {
		var visible bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM system.connector_inbox WHERE id = $1)`, inboxID).Scan(&visible); err != nil || !visible {
			t.Errorf("inbox row %s not visible on the enqueue transaction: %v", inboxID, err)
		}
		enqueued = append(enqueued, inboxID)
		return nil
	}

	id, inserted, err := env.store.AcceptDelivery(t.Context(), tenantID, "connector_paystack", "evt_1", []byte(`{}`), enqueue)
	if err != nil || !inserted || len(enqueued) != 1 || enqueued[0] != id {
		t.Fatalf("first AcceptDelivery() = %q, %v, %v with enqueued %v; want one enqueue of the new row", id, inserted, err, enqueued)
	}

	_, inserted, err = env.store.AcceptDelivery(t.Context(), tenantID, "connector_paystack", "evt_1", []byte(`{}`), enqueue)
	if err != nil || inserted || len(enqueued) != 1 {
		t.Errorf("duplicate AcceptDelivery() = %v, %v with enqueued %v; want no insert and no enqueue", inserted, err, enqueued)
	}
}

func TestAcceptDelivery_EnqueueFailureRollsBackTheInsert(t *testing.T) {
	env := openTestStore(t)
	tenantID := env.createTenant(t)

	_, _, err := env.store.AcceptDelivery(t.Context(), tenantID, "connector_paystack", "evt_1", []byte(`{}`),
		func(context.Context, *sql.Tx, string) error { return errors.New("queue down") })
	if err == nil {
		t.Fatal("AcceptDelivery() succeeded although enqueue failed")
	}

	_, inserted, err := env.store.InsertInbox(t.Context(), tenantID, "connector_paystack", "evt_1", []byte(`{}`))
	if err != nil || !inserted {
		t.Errorf("redelivery after a failed enqueue: InsertInbox() = %v, %v; want the event to be insertable again", inserted, err)
	}
}

func TestInboxRowAccess_IsScopedToTenantAndModule(t *testing.T) {
	env := openTestStore(t)
	tenantID := env.createTenant(t)
	otherTenantID := env.createTenant(t)
	id, _, err := env.store.InsertInbox(t.Context(), tenantID, "connector_paystack", "evt_1", []byte(`{"reference":"ref_1"}`))
	if err != nil {
		t.Fatalf("InsertInbox() error: %v", err)
	}

	row, err := env.store.GetInbox(t.Context(), tenantID, "connector_paystack", id)
	if err != nil || row.ProviderEventID != "evt_1" || row.Status != StatusPending || !strings.Contains(string(row.Payload), "ref_1") {
		t.Fatalf("GetInbox() = %+v, %v; want the pending row", row, err)
	}

	for _, tt := range []struct{ name, tenant, module, id string }{
		{"another tenant", otherTenantID, "connector_paystack", id},
		{"another module", tenantID, "connector_stripe", id},
		{"unknown id", tenantID, "connector_paystack", "00000000-0000-7000-8000-000000000000"},
		{"not a uuid", tenantID, "connector_paystack", "not-a-uuid"},
	} {
		if _, err := env.store.GetInbox(t.Context(), tt.tenant, tt.module, tt.id); !errors.Is(err, ErrInboxNotFound) {
			t.Errorf("%s: GetInbox() error = %v, want ErrInboxNotFound", tt.name, err)
		}
		if err := env.store.MarkProcessed(t.Context(), tt.tenant, tt.module, tt.id); !errors.Is(err, ErrInboxNotFound) {
			t.Errorf("%s: MarkProcessed() error = %v, want ErrInboxNotFound", tt.name, err)
		}
		if err := env.store.MarkFailed(t.Context(), tt.tenant, tt.module, tt.id, "why"); !errors.Is(err, ErrInboxNotFound) {
			t.Errorf("%s: MarkFailed() error = %v, want ErrInboxNotFound", tt.name, err)
		}
	}
}

func TestMarkProcessedAndFailed(t *testing.T) {
	env := openTestStore(t)
	tenantID := env.createTenant(t)
	id, _, err := env.store.InsertInbox(t.Context(), tenantID, "connector_paystack", "evt_1", []byte(`{}`))
	if err != nil {
		t.Fatalf("InsertInbox() error: %v", err)
	}
	state := func() (status string, reason sql.NullString, processedAt sql.NullTime) {
		t.Helper()
		err := env.conn.QueryRowContext(t.Context(),
			`SELECT status, failure_reason, processed_at FROM system.connector_inbox WHERE id = $1`, id).Scan(&status, &reason, &processedAt)
		if err != nil {
			t.Fatalf("read row state: %v", err)
		}
		return status, reason, processedAt
	}

	if err := env.store.MarkFailed(t.Context(), tenantID, "connector_paystack", id, "invalid payload"); err != nil {
		t.Fatalf("MarkFailed() error: %v", err)
	}
	if status, reason, _ := state(); status != StatusFailed || reason.String != "invalid payload" {
		t.Errorf("after MarkFailed: status %q reason %q, want failed with the reason", status, reason.String)
	}

	if err := env.store.MarkProcessed(t.Context(), tenantID, "connector_paystack", id); err != nil {
		t.Fatalf("MarkProcessed() error: %v", err)
	}
	status, reason, processedAt := state()
	if status != StatusProcessed || reason.Valid || !processedAt.Valid {
		t.Errorf("after MarkProcessed: status %q reason %+v processed_at %+v, want processed, no reason, timestamped", status, reason, processedAt)
	}

	if err := env.store.MarkFailed(t.Context(), tenantID, "connector_paystack", id, "late retry"); err != nil {
		t.Errorf("MarkFailed() on a processed row error: %v, want a no-op", err)
	}
	if status, reason, _ := state(); status != StatusProcessed || reason.Valid {
		t.Errorf("after MarkFailed on a processed row: status %q reason %+v, want it left processed", status, reason)
	}

	if err := env.store.MarkProcessed(t.Context(), tenantID, "connector_paystack", id); err != nil {
		t.Errorf("second MarkProcessed() error: %v, want a no-op", err)
	}
	if _, _, again := state(); !again.Time.Equal(processedAt.Time) {
		t.Error("second MarkProcessed() moved processed_at")
	}
}
