package mfaenroll

import (
	"bytes"
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"uuid"

	"github.com/descope/virtualwebauthn"
	"github.com/djangbahevans/goerp/internal/engine/auth/mfafactors"
	"github.com/djangbahevans/goerp/internal/engine/auth/mfareverify"
	"github.com/djangbahevans/goerp/internal/engine/auth/mfatoken"
	"github.com/djangbahevans/goerp/internal/engine/auth/mfaverify"
	"github.com/djangbahevans/goerp/internal/engine/authaudit"
	"github.com/djangbahevans/goerp/internal/engine/authaudit/audittest"
	"github.com/djangbahevans/goerp/internal/engine/mfa"
	"github.com/djangbahevans/goerp/internal/engine/mfa/enforce"
	"github.com/djangbahevans/goerp/internal/engine/mfa/lockout"
	"github.com/djangbahevans/goerp/internal/engine/mfa/webauthn"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
)

type passkeyFixture struct {
	*fixture
	verify        *mfaverify.Handler
	reverify      *mfareverify.Handler
	mfaTokens     *mfatoken.Codec
	rp            virtualwebauthn.RelyingParty
	authenticator virtualwebauthn.Authenticator
	credential    virtualwebauthn.Credential
}

func newPasskeyFixture(t *testing.T) *passkeyFixture {
	t.Helper()

	f := newFixture(t)
	svc, err := webauthn.NewService(webauthn.Config{RPID: "goerp.test", BaseURL: "https://goerp.test"}, f.handlers.mfa, f.rowKeys, f.cache, f.handlers.sessions)
	if err != nil {
		t.Fatal(err)
	}

	audit := authaudit.NewStore(f.conn, tenant.NewStore(f.conn))
	if err := audit.Bootstrap(t.Context()); err != nil {
		t.Fatal(err)
	}

	f.handlers.webauthn = svc
	f.handlers.audit = audit
	t.Cleanup(func() { _, _ = f.conn.Exec(`DELETE FROM system.auth_audit_log WHERE tenant_id=$1`, f.tenantID) })
	codec := mfatoken.NewCodec(&mfatoken.Key{KeyID: uuid.NewV7().String(), Secret: bytes.Repeat([]byte{42}, 32)})

	return &passkeyFixture{
		fixture:       f,
		verify:        mfaverify.NewHandler(codec, f.cache, f.handlers.totp, f.handlers.recovery, tenant.NewStore(f.conn), f.issuer, f.handlers.mfa, svc, audit),
		reverify:      mfareverify.NewHandler(f.handlers.tenants, f.checker, f.handlers.sessions, f.issuer, f.handlers.totp, f.handlers.recovery, lockout.NewCounter(f.cache), f.handlers.mfa, svc, audit),
		mfaTokens:     codec,
		rp:            virtualwebauthn.RelyingParty{ID: "goerp.test", Name: "GoERP", Origin: "https://" + f.domain},
		authenticator: virtualwebauthn.NewAuthenticator(),
		credential:    virtualwebauthn.NewCredential(virtualwebauthn.KeyTypeEC2),
	}
}

func (f *passkeyFixture) request(t *testing.T, handler http.HandlerFunc, path, token string, body any, want int) map[string]any {
	t.Helper()

	blob, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(blob))
	req.Host = f.domain
	req.RemoteAddr = "203.0.113.7:54321"
	req.Header.Set("Origin", f.rp.Origin)
	req.Header.Set("X-Client-Type", "cli")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != want {
		t.Fatalf("%s: status %d, want %d: %s", path, rec.Code, want, rec.Body.String())
	}

	var result map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}

	return result
}

func (f *passkeyFixture) enroll(t *testing.T, token string) map[string]any {
	t.Helper()

	begin := f.request(t, f.handlers.BeginWebAuthn, "/auth/mfa/enroll/webauthn", token, map[string]any{}, 200)
	options, err := json.Marshal(begin["options"])
	if err != nil {
		t.Fatal(err)
	}

	parsed, err := virtualwebauthn.ParseAttestationOptions(string(options))
	if err != nil {
		t.Fatal(err)
	}

	f.authenticator.AddCredential(f.credential)
	response := virtualwebauthn.CreateAttestationResponse(f.rp, f.authenticator, f.credential, *parsed)
	return f.request(t, f.handlers.ConfirmWebAuthn, "/auth/mfa/enroll/webauthn/confirm", token,
		map[string]any{"ceremony_id": begin["ceremony_id"], "response": jsontext.Value(response), "label": "Laptop"}, 200)
}

func (f *passkeyFixture) assertion(t *testing.T, options map[string]any, count uint32) jsontext.Value {
	t.Helper()

	blob, err := json.Marshal(options["options"])
	if err != nil {
		t.Fatal(err)
	}

	parsed, err := virtualwebauthn.ParseAssertionOptions(string(blob))
	if err != nil {
		t.Fatal(err)
	}

	f.credential.Counter = count
	return jsontext.Value(virtualwebauthn.CreateAssertionResponse(f.rp, f.authenticator, f.credential, *parsed))
}

func (f *passkeyFixture) loginToken(t *testing.T) string {
	t.Helper()

	token, _, err := f.mfaTokens.Issue(f.userID, f.tenantID, f.rp.Origin, mfatoken.IssueOptions{})
	if err != nil {
		t.Fatal(err)
	}

	return token
}

func TestWebAuthnEnrollmentAndLogin(t *testing.T) {
	f := newPasskeyFixture(t)
	f.requireMFA(t)
	token := f.login(t, f.userID)
	result := f.enroll(t, token)
	if len(result["recovery_codes"].([]any)) != 10 {
		t.Fatal("first factor must return ten recovery codes")
	}

	if f.countActive(t, f.userID, mfa.CredentialWebAuthn) != 1 {
		t.Fatal("passkey not stored")
	}

	authCtx := f.authContext(t, result["access_token"].(string))
	if !slices.Contains(authCtx.AMR, "webauthn") || !authCtx.MFAVerified {
		t.Fatalf("MFA method = %q", authCtx.AMR)
	}

	factors := mfafactors.NewHandlers(f.handlers.tenants, f.checker, f.handlers.mfa, enforce.NewStore(f.config), f.handlers.totp, f.handlers.recovery, lockout.NewCounter(f.cache), nil, nil, nil)
	listed := f.request(t, factors.List, "/auth/mfa/factors", result["access_token"].(string), map[string]any{}, 200)
	if listed["factors"].([]any)[0].(map[string]any)["type"] != "webauthn" {
		t.Fatalf("listed factors = %v", listed)
	}

	decision, err := f.checker.EnforceMFA(t.Context(), "/items", f.tenantID, authCtx)
	if err != nil || decision != enforce.Allowed {
		t.Fatalf("enforcement = %s, %v", decision, err)
	}

	audittest.AssertLatest(t, f.conn, f.tenantID, "mfa.enrolled", f.userID, f.userID)
	f.request(t, f.handlers.BeginWebAuthn, "/auth/mfa/enroll/webauthn", token, map[string]any{}, 403)

	mfaToken := f.loginToken(t)
	options := f.request(t, f.verify.Options, "/auth/mfa/webauthn/options", "", map[string]any{"mfa_token": mfaToken}, 200)
	response := f.assertion(t, options, 5)
	body := map[string]any{"mfa_token": mfaToken, "type": "webauthn", "ceremony_id": options["ceremony_id"], "response": response}
	verified := f.request(t, f.verify.ServeHTTP, "/auth/mfa/verify", "", body, 200)
	var method, credentialID string
	if err := f.conn.QueryRow(`
  SELECT mfa_method, mfa_credential_id FROM system.sessions
  WHERE user_id=$1 AND id<>$2
  ORDER BY created_at DESC LIMIT 1`, f.userID, authCtx.SessionID).Scan(&method, &credentialID); err != nil {
		t.Fatal(err)
	}

	if method != "webauthn" || credentialID == "" {
		t.Fatalf("session assurance = %q, %q", method, credentialID)
	}

	f.request(t, f.verify.Options, "/auth/mfa/webauthn/options", "", map[string]any{"mfa_token": mfaToken}, 401)
	f.request(t, f.verify.ServeHTTP, "/auth/mfa/verify", "", body, 401)

	token = verified["access_token"].(string)
	options = f.request(t, f.reverify.Options, "/auth/mfa/reverify/webauthn/options", token, map[string]any{}, 200)
	result = f.request(t, f.reverify.ServeHTTP, "/auth/mfa/reverify", token, map[string]any{"type": "webauthn", "ceremony_id": options["ceremony_id"], "response": f.assertion(t, options, 6)}, 200)
	if result["access_token"] == nil {
		t.Fatal("reverify did not reissue access token")
	}
}

func TestWebAuthnCloneAudit(t *testing.T) {
	for _, duringLogin := range []bool{true, false} {
		t.Run(map[bool]string{true: "login", false: "reverify"}[duringLogin], func(t *testing.T) {
			f := newPasskeyFixture(t)
			result := f.enroll(t, f.login(t, f.userID))
			token := result["access_token"].(string)
			options := f.request(t, f.reverify.Options, "/auth/mfa/reverify/webauthn/options", token, map[string]any{}, 200)
			result = f.request(t, f.reverify.ServeHTTP, "/auth/mfa/reverify", token, map[string]any{"type": "webauthn", "ceremony_id": options["ceremony_id"], "response": f.assertion(t, options, 5)}, 200)
			token = result["access_token"].(string)
			handler, optionsHandler, path, optionsPath := f.reverify.ServeHTTP, f.reverify.Options, "/auth/mfa/reverify", "/auth/mfa/reverify/webauthn/options"
			body := map[string]any{"type": "webauthn"}
			optionsBody := map[string]any{}
			actor := f.userID
			if duringLogin {
				handler, optionsHandler, path, optionsPath = f.verify.ServeHTTP, f.verify.Options, "/auth/mfa/verify", "/auth/mfa/webauthn/options"
				body["mfa_token"] = f.loginToken(t)
				optionsBody["mfa_token"] = body["mfa_token"]
				token = ""
				actor = ""
			}

			options = f.request(t, optionsHandler, optionsPath, token, optionsBody, 200)
			body["ceremony_id"] = options["ceremony_id"]
			body["response"] = f.assertion(t, options, 3)
			f.request(t, handler, path, token, body, 401)
			audittest.AssertLatest(t, f.conn, f.tenantID, "mfa.clone_suspected", f.userID, actor)
			if f.countActive(t, f.userID, mfa.CredentialWebAuthn) != 0 {
				t.Fatal("cloned passkey remains active")
			}

			f.request(t, handler, path, token, body, 401)
			var count int
			if err := f.conn.QueryRow(`
    SELECT count(*) FROM system.auth_audit_log
    WHERE user_id=$1 AND event_type='mfa.clone_suspected'
     AND metadata->>'credential_id' IN (
      SELECT id::text FROM system.user_mfa WHERE user_id=$1 AND revoked_at IS NOT NULL
     )`, f.userID).Scan(&count); err != nil {
				t.Fatal(err)
			}

			if count != 1 {
				t.Fatalf("clone audit count = %d", count)
			}
		})
	}
}

func TestWebAuthnInvalidAssertionsLockOut(t *testing.T) {
	f := newPasskeyFixture(t)
	token := f.enroll(t, f.login(t, f.userID))["access_token"].(string)
	for range lockout.MaxAttempts {
		options := f.request(t, f.reverify.Options, "/auth/mfa/reverify/webauthn/options", token, map[string]any{}, 200)
		f.request(t, f.reverify.ServeHTTP, "/auth/mfa/reverify", token, map[string]any{"type": "webauthn", "ceremony_id": options["ceremony_id"], "response": map[string]any{}}, 401)
	}

	f.request(t, f.reverify.Options, "/auth/mfa/reverify/webauthn/options", token, map[string]any{}, 423)
	f.request(t, f.verify.Options, "/auth/mfa/webauthn/options", "", map[string]any{"mfa_token": f.loginToken(t)}, 423)
}

func TestWebAuthnCeremonyBoundToLoginAndSession(t *testing.T) {
	f := newPasskeyFixture(t)
	token := f.enroll(t, f.login(t, f.userID))["access_token"].(string)
	first := f.loginToken(t)
	options := f.request(t, f.verify.Options, "/auth/mfa/webauthn/options", "", map[string]any{"mfa_token": first}, 200)

	second := f.loginToken(t)
	f.request(t, f.verify.ServeHTTP, "/auth/mfa/verify", "", map[string]any{"mfa_token": second, "type": "webauthn", "ceremony_id": options["ceremony_id"], "response": f.assertion(t, options, 1)}, 401)

	options = f.request(t, f.reverify.Options, "/auth/mfa/reverify/webauthn/options", token, map[string]any{}, 200)
	otherSession := f.login(t, f.userID)
	f.request(t, f.reverify.ServeHTTP, "/auth/mfa/reverify", otherSession, map[string]any{"type": "webauthn", "ceremony_id": options["ceremony_id"], "response": f.assertion(t, options, 2)}, 401)

	locked, _, err := f.cache.Get(t.Context(), "auth:mfa_attempts:"+f.userID+":"+f.tenantID)
	if err != nil {
		t.Fatal(err)
	}

	if locked != "2" {
		t.Fatalf("failure count = %q, want 2", locked)
	}
}

func TestWebAuthnInvalidLoginAssertionCountsTowardLockout(t *testing.T) {
	f := newPasskeyFixture(t)
	f.enroll(t, f.login(t, f.userID))
	mfaToken := f.loginToken(t)
	options := f.request(t, f.verify.Options, "/auth/mfa/webauthn/options", "", map[string]any{"mfa_token": mfaToken}, 200)

	var response map[string]any
	if err := json.Unmarshal(f.assertion(t, options, 1), &response); err != nil {
		t.Fatal(err)
	}

	signed := response["response"].(map[string]any)
	signature := signed["signature"].(string)
	if signature[0] == 'A' {
		signature = "B" + signature[1:]
	} else {
		signature = "A" + signature[1:]
	}
	signed["signature"] = signature

	f.request(t, f.verify.ServeHTTP, "/auth/mfa/verify", "", map[string]any{"mfa_token": mfaToken, "type": "webauthn", "ceremony_id": options["ceremony_id"], "response": response}, 401)
	count, _, err := f.cache.Get(t.Context(), "auth:mfa_attempts:"+f.userID+":"+f.tenantID)
	if err != nil {
		t.Fatal(err)
	}

	if count != "1" {
		t.Fatalf("failure count = %q, want 1", count)
	}
}

func TestWebAuthnAdditionalEnrollmentKeepsRecoveryCodes(t *testing.T) {
	f := newPasskeyFixture(t)
	token := f.enroll(t, f.login(t, f.userID))["access_token"].(string)
	f.credential = virtualwebauthn.NewCredential(virtualwebauthn.KeyTypeEC2)
	result := f.enroll(t, token)

	if result["recovery_codes"] != nil {
		t.Fatal("additional enrollment must return null recovery codes")
	}

	if f.countActive(t, f.userID, mfa.CredentialRecoveryCode) != 10 {
		t.Fatal("additional enrollment replaced or added recovery codes")
	}

	if f.countActive(t, f.userID, mfa.CredentialWebAuthn) != 2 {
		t.Fatal("second passkey not stored")
	}
}

func TestWebAuthnEnrollmentAfterResetCreatesTenantFactor(t *testing.T) {
	f := newPasskeyFixture(t)
	if err := f.handlers.mfa.WithTx(t.Context(), func(tx *sql.Tx) error {
		return f.handlers.mfa.SetResetTx(t.Context(), tx, f.userID, f.tenantID, true)
	}); err != nil {
		t.Fatal(err)
	}

	f.requireMFA(t)
	result := f.enroll(t, f.login(t, f.userID))
	accepted, err := f.handlers.mfa.ListAccepted(t.Context(), f.userID, mfa.Scope{TenantID: f.tenantID})
	if err != nil {
		t.Fatal(err)
	}

	if len(accepted) != 11 || !mfa.HasFactor(accepted) {
		t.Fatalf("accepted factors = %v, want passkey and ten recovery codes", accepted)
	}

	for _, factor := range accepted {
		if factor.TenantID == nil || *factor.TenantID != f.tenantID {
			t.Fatalf("reset enrollment produced a platform credential: %s", factor.ID)
		}
	}

	decision, err := f.checker.EnforceMFA(t.Context(), "/items", f.tenantID, f.authContext(t, result["access_token"].(string)))
	if err != nil || decision != enforce.Allowed {
		t.Fatalf("enforcement = %s, %v", decision, err)
	}
}
