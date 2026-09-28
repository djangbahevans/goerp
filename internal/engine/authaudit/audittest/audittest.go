// Package audittest holds test assertions over system.auth_audit_log
// rows, shared by the packages that write them.
package audittest

import (
	"database/sql"
	"encoding/json/v2"
	"testing"
)

// AssertLatest checks the newest eventType row in tenantID ("" for a row
// with no tenant) against auth-internals.md §17 "Who an event is about,
// and who caused it": its user_id is wantUserID and its actor_user_id is
// wantActorUserID, "" meaning NULL, and its metadata never names the actor
// as performed_by. A non-empty wantUserID also selects the row, so tests
// sharing a tenant (or none) don't read each other's rows.
func AssertLatest(t testing.TB, conn *sql.DB, tenantID, eventType, wantUserID, wantActorUserID string) {
	t.Helper()
	var userID, actorUserID sql.NullString
	var metadata []byte
	err := conn.QueryRow(`
		SELECT user_id, actor_user_id, metadata FROM system.auth_audit_log
		WHERE event_type = $1
		  AND tenant_id IS NOT DISTINCT FROM NULLIF($2, '')::uuid
		  AND ($3 = '' OR user_id = NULLIF($3, '')::uuid)
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, eventType, tenantID, wantUserID).Scan(&userID, &actorUserID, &metadata)
	if err != nil {
		t.Fatalf("read latest %s audit row: %v", eventType, err)
	}
	if userID.String != wantUserID || actorUserID.String != wantActorUserID {
		t.Errorf("%s user_id/actor_user_id = %q/%q, want %q/%q", eventType, userID.String, actorUserID.String, wantUserID, wantActorUserID)
	}
	if metadata == nil {
		return
	}
	var fields map[string]any
	if err := json.Unmarshal(metadata, &fields); err != nil {
		t.Fatalf("decode %s audit metadata: %v", eventType, err)
	}
	if _, ok := fields["performed_by"]; ok {
		t.Errorf("%s metadata %s contains performed_by", eventType, metadata)
	}
}
