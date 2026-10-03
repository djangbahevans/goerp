package mfa

import (
	"database/sql"
	"slices"
	"testing"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/auth/session"
	"github.com/djangbahevans/goerp/internal/engine/mfa/mfatest"
)

func TestListAccepted_TenantIsolationAndResetBarrier(t *testing.T) {
	e := openTestEnv(t)
	userID := e.createUser(t)
	other := mfatest.NewTenant(t, e.conn)
	mfatest.AddMember(t, e.conn, other, userID)
	var platform, own, foreign *Credential
	err := e.store.WithTx(t.Context(), func(tx *sql.Tx) error {
		var err error
		platform, err = e.store.InsertScopedTx(t.Context(), tx, userID, nil, CredentialTOTP, []byte("platform"), nil)
		if err != nil {
			return err
		}

		own, err = e.store.InsertScopedTx(t.Context(), tx, userID, new(e.tenant.ID), CredentialWebAuthn, []byte("own"), nil)
		if err != nil {
			return err
		}

		foreign, err = e.store.InsertScopedTx(t.Context(), tx, userID, new(other.ID), CredentialTOTP, []byte("foreign"), nil)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	assertAccepted := func(scope Scope, expected ...string) {
		t.Helper()
		creds, err := e.store.ListAccepted(t.Context(), userID, scope)
		if err != nil {
			t.Fatal(err)
		}

		ids := make([]string, len(creds))
		for i, c := range creds {
			ids[i] = c.ID
		}

		slices.Sort(ids)
		slices.Sort(expected)
		if !slices.Equal(ids, expected) {
			t.Fatalf("accepted = %v, want %v", ids, expected)
		}
	}

	assertAccepted(Scope{TenantID: e.tenant.ID}, platform.ID, own.ID)
	assertAccepted(Scope{TenantID: other.ID}, platform.ID, foreign.ID)
	assertAccepted(Scope{TenantID: e.tenant.ID, PlatformOnly: true}, platform.ID)
	if err := e.store.WithTx(t.Context(), func(tx *sql.Tx) error {
		return e.store.SetResetTx(t.Context(), tx, userID, e.tenant.ID, true)
	}); err != nil {
		t.Fatal(err)
	}
	assertAccepted(Scope{TenantID: e.tenant.ID})
	assertAccepted(Scope{TenantID: e.tenant.ID, PlatformOnly: true})
	assertAccepted(Scope{TenantID: other.ID}, platform.ID, foreign.ID)
}

func TestEnrollmentTenant_UsesStoredProofAndAllAccountFactors(t *testing.T) {
	for _, tc := range []struct {
		name          string
		existingScope string
		proof         bool
		reset         bool
		wantTenant    bool
	}{
		{name: "first factor"},
		{name: "reset with no remaining factors", reset: true, wantTenant: true},
		{name: "platform proof", existingScope: "platform", proof: true},
		{name: "password only with platform factor", existingScope: "platform", wantTenant: true},
		{name: "tenant proof", existingScope: "own", proof: true, wantTenant: true},
		{name: "only foreign tenant factor", existingScope: "foreign", wantTenant: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := openTestEnv(t)
			userID := e.createUser(t)
			ss := session.NewStore(e.conn)
			if err := ss.Bootstrap(t.Context()); err != nil {
				t.Fatal(err)
			}

			var proofID string
			if tc.existingScope != "" {
				var tid *string
				if tc.existingScope == "own" {
					tid = new(e.tenant.ID)
				}

				if tc.existingScope == "foreign" {
					tid = new(mfatest.NewTenant(t, e.conn).ID)
				}

				if err := e.store.WithTx(t.Context(), func(tx *sql.Tx) error {
					c, err := e.store.InsertScopedTx(t.Context(), tx, userID, tid, CredentialTOTP, []byte("secret"), nil)
					if err == nil && tc.proof {
						proofID = c.ID
					}

					return err
				}); err != nil {
					t.Fatal(err)
				}
			}

			id := uuid.NewV7().String()
			row := session.Row{ID: id, UserID: userID, TenantID: e.tenant.ID, DeviceID: uuid.NewV7().String(), RefreshHash: id, ExpiresAt: time.Now().Add(time.Hour)}
			if proofID != "" {
				row.MFACredentialID = proofID
				row.MFAMethod = "totp"
				row.MFAVerifiedAt = new(time.Now())
			}

			if err := ss.Insert(t.Context(), row); err != nil {
				t.Fatal(err)
			}

			t.Cleanup(func() {
				_, _ = e.conn.Exec(`DELETE FROM system.sessions WHERE id = $1`, id)
			})
			if err := e.store.WithTx(t.Context(), func(tx *sql.Tx) error {
				if err := e.store.LockUserTx(t.Context(), tx, userID); err != nil {
					return err
				}

				if tc.reset {
					if err := e.store.SetResetTx(t.Context(), tx, userID, e.tenant.ID, true); err != nil {
						return err
					}
				}

				tid, err := e.store.EnrollmentTenantTx(t.Context(), tx, userID, e.tenant.ID, id)
				if err == nil && ((tid != nil) != tc.wantTenant || tid != nil && *tid != e.tenant.ID) {
					t.Errorf("scope = %v, wantTenant = %v", tid, tc.wantTenant)
				}

				return err
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
