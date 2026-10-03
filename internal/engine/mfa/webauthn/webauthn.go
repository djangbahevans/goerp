// Package webauthn implements tenant-bound WebAuthn registration and login ceremonies.
package webauthn

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"time"
	"uuid"

	"github.com/go-webauthn/webauthn/protocol"
	wan "github.com/go-webauthn/webauthn/webauthn"
	"github.com/rs/zerolog/log"

	"github.com/djangbahevans/goerp/internal/engine/auth/rowcrypt"
	"github.com/djangbahevans/goerp/internal/engine/auth/session"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/mfa"
)

type Config struct {
	RPID          string   `env:"GOERP_WEBAUTHN_RP_ID,required"`
	RPDisplayName string   `env:"GOERP_WEBAUTHN_RP_DISPLAY_NAME" envDefault:"GoERP"`
	RPOrigins     []string `env:"GOERP_WEBAUTHN_RP_ORIGINS,required" envSeparator:","`
}

const ceremonyTTL = 5 * time.Minute

var (
	// ErrCeremonyExpired covers both an unknown and an expired ceremony
	// ID — Redis TTL expiry and "never existed" look identical from this
	// package's side, and callers don't need to distinguish them.
	ErrCeremonyExpired = errors.New("webauthn ceremony expired or not found")
	// ErrCeremonyUserMismatch means ceremonyID exists but was started for
	// a different user — never trust the caller-supplied userID alone.
	ErrCeremonyUserMismatch = errors.New("webauthn ceremony does not belong to this user and tenant")
	// ErrNoEnrolledCredentials means the user has no active WebAuthn
	// factor to authenticate a login ceremony against.
	ErrNoEnrolledCredentials = errors.New("user has no enrolled webauthn credentials")
)

// CloneDetectedError is returned by FinishLogin when the submitted
// assertion's sign count signals a possible cloned authenticator or
// replayed assertion (auth-internals.md §8's clone-detection check,
// performed internally by the library's Authenticator.UpdateCounter and
// surfaced here via Authenticator.CloneWarning). The matched credential
// has already been revoked by the time this error is returned.
type CloneDetectedError struct {
	CredentialID string // the revoked system.user_mfa row id
}

func (e *CloneDetectedError) Error() string {
	return fmt.Sprintf("webauthn clone or replay suspected for credential %s; credential revoked", e.CredentialID)
}

type Service struct {
	webAuthn *wan.WebAuthn
	store    *mfa.Store
	keys     *rowcrypt.RowKeySet
	cache    *cache.Client
	sessions *session.Store
}

func NewService(cfg Config, store *mfa.Store, keys *rowcrypt.RowKeySet, cacheClient *cache.Client, sessions *session.Store) (*Service, error) {
	webAuthn, err := wan.New(&wan.Config{
		RPID:          cfg.RPID,
		RPDisplayName: cfg.RPDisplayName,
		RPOrigins:     cfg.RPOrigins,
	})
	if err != nil {
		return nil, fmt.Errorf("configure webauthn relying party: %w", err)
	}

	return &Service{webAuthn: webAuthn, store: store, keys: keys, cache: cacheClient, sessions: sessions}, nil
}

// BeginRegistration excludes already accepted credentials to prevent duplicate device registration.
func (s *Service) BeginRegistration(ctx context.Context, userID, accountName string, scope mfa.Scope) (optionsJSON []byte, ceremonyID string, err error) {
	user, err := s.loadUser(ctx, userID, accountName, scope)
	if err != nil {
		return nil, "", err
	}

	exclude := make([]protocol.CredentialDescriptor, len(user.records))
	for i, r := range user.records {
		exclude[i] = r.credential.Descriptor()
	}

	creation, sessionData, err := s.webAuthn.BeginRegistration(user, wan.WithExclusions(exclude))
	if err != nil {
		return nil, "", fmt.Errorf("begin webauthn registration: %w", err)
	}

	ceremonyID = uuid.New().String()
	if err := s.storeSession(ctx, regKey(ceremonyID), userID, *sessionData, scope.TenantID); err != nil {
		return nil, "", err
	}

	// v2 omits a zero-valued AuthenticatorSelection that v1 sent as {} —
	// spec-equivalent to any WebAuthn client. Escape options match v1's
	// Encoder defaults, since optionsJSON reaches an HTTP client verbatim.
	optionsJSON, err = json.Marshal(creation, jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true))
	if err != nil {
		return nil, "", fmt.Errorf("marshal webauthn registration options: %w", err)
	}

	return optionsJSON, ceremonyID, nil
}

// FinishRegistration consumes the account-and-tenant-bound ceremony on every completion attempt.
func (s *Service) FinishRegistration(ctx context.Context, userID, ceremonyID, accountName string, responseJSON []byte, label *string, scope mfa.Scope, sessionID string) (*mfa.Credential, error) {
	key := regKey(ceremonyID)
	sess, sessErr := s.loadSession(ctx, key)
	defer func() {
		_ = s.cache.Delete(ctx, key)
	}()
	if sessErr != nil {
		return nil, sessErr
	}

	if sess.UserID != userID || sess.TenantID != scope.TenantID {
		return nil, ErrCeremonyUserMismatch
	}

	user, err := s.loadUser(ctx, userID, accountName, scope)
	if err != nil {
		return nil, err
	}

	parsed, err := protocol.ParseCredentialCreationResponseBytes(responseJSON)
	if err != nil {
		return nil, fmt.Errorf("parse webauthn registration response: %w", err)
	}

	credential, err := s.webAuthn.CreateCredential(user, sess.Data, parsed)
	if err != nil {
		return nil, fmt.Errorf("verify webauthn registration: %w", err)
	}

	ciphertext, err := s.encryptCredential(credential)
	if err != nil {
		return nil, err
	}

	var row *mfa.Credential
	err = s.store.WithTx(ctx, func(tx *sql.Tx) error {
		if err := s.store.LockUserTx(ctx, tx, userID); err != nil {
			return err
		}

		factorTenant, err := s.store.EnrollmentTenantTx(ctx, tx, userID, scope.TenantID, sessionID)
		if err != nil {
			return err
		}

		row, err = s.store.InsertScopedTx(ctx, tx, userID, factorTenant, mfa.CredentialWebAuthn, ciphertext, label)
		if err != nil {
			return err
		}

		if factorTenant != nil {
			if err := s.store.SetResetTx(ctx, tx, userID, scope.TenantID, false); err != nil {
				return err
			}
		}

		_, err = s.sessions.UpdateMFAAssuranceTx(ctx, tx, sessionID, "webauthn", time.Now(), row.ID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("store webauthn credential: %w", err)
	}

	return row, nil
}

func (s *Service) BeginLogin(ctx context.Context, userID, accountName string, scope mfa.Scope) (optionsJSON []byte, ceremonyID string, err error) {
	user, err := s.loadUser(ctx, userID, accountName, scope)
	if err != nil {
		return nil, "", err
	}

	if len(user.records) == 0 {
		return nil, "", ErrNoEnrolledCredentials
	}

	assertion, sessionData, err := s.webAuthn.BeginLogin(user)
	if err != nil {
		return nil, "", fmt.Errorf("begin webauthn login: %w", err)
	}

	ceremonyID = uuid.New().String()
	if err := s.storeSession(ctx, authKey(ceremonyID), userID, *sessionData, scope.TenantID); err != nil {
		return nil, "", err
	}

	// Same HTML/JS-escape parity as BeginRegistration's optionsJSON above.
	optionsJSON, err = json.Marshal(assertion, jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true))
	if err != nil {
		return nil, "", fmt.Errorf("marshal webauthn login options: %w", err)
	}

	return optionsJSON, ceremonyID, nil
}

// FinishLogin consumes the ceremony on every completion attempt.
// A clone warning revokes the matched credential before returning CloneDetectedError.
func (s *Service) FinishLogin(ctx context.Context, userID, ceremonyID, accountName string, responseJSON []byte, scope mfa.Scope) (credentialID string, err error) {
	key := authKey(ceremonyID)
	sess, sessErr := s.loadSession(ctx, key)
	defer func() {
		_ = s.cache.Delete(ctx, key)
	}()
	if sessErr != nil {
		return "", sessErr
	}

	if sess.UserID != userID || sess.TenantID != scope.TenantID {
		return "", ErrCeremonyUserMismatch
	}

	user, err := s.loadUser(ctx, userID, accountName, scope)
	if err != nil {
		return "", err
	}

	parsed, err := protocol.ParseCredentialRequestResponseBytes(responseJSON)
	if err != nil {
		return "", fmt.Errorf("parse webauthn login response: %w", err)
	}

	matched, err := s.webAuthn.ValidateLogin(user, sess.Data, parsed)
	if err != nil {
		return "", fmt.Errorf("validate webauthn login: %w", err)
	}

	mfaID := user.mfaIDFor(matched.ID)
	if mfaID == "" {
		return "", fmt.Errorf("validated credential %x matched no stored user_mfa row", matched.ID)
	}

	if matched.Authenticator.CloneWarning {
		if revokeErr := s.store.Revoke(ctx, mfaID); revokeErr != nil {
			return "", fmt.Errorf("revoke suspected-cloned webauthn credential: %w", revokeErr)
		}

		log.Warn().
			Str("event", "mfa.clone_suspected").
			Str("user_id", userID).
			Str("credential_id", mfaID).
			Msg("webauthn sign count did not increase; possible cloned authenticator, credential revoked")
		return "", &CloneDetectedError{CredentialID: mfaID}
	}

	ciphertext, err := s.encryptCredential(matched)
	if err != nil {
		return "", err
	}

	if err := s.store.UpdateCredentialAfterUse(ctx, mfaID, ciphertext); err != nil {
		return "", fmt.Errorf("update webauthn credential after login: %w", err)
	}

	return mfaID, nil
}

func (s *Service) encryptCredential(credential *wan.Credential) ([]byte, error) {
	blob, err := json.Marshal(credential)
	if err != nil {
		return nil, fmt.Errorf("marshal webauthn credential: %w", err)
	}

	ciphertext, err := s.keys.Encrypt(blob)
	if err != nil {
		return nil, fmt.Errorf("encrypt webauthn credential: %w", err)
	}

	return ciphertext, nil
}

// credentialRecord pairs a decrypted wan.Credential with the
// system.user_mfa row id it was loaded from, so a credential the library
// hands back after Begin/Finish can be traced back to the row to
// revoke/update.
type credentialRecord struct {
	mfaID      string
	credential wan.Credential
}

type webauthnUser struct {
	id          string
	accountName string
	records     []credentialRecord
}

func (u *webauthnUser) WebAuthnID() []byte {
	return []byte(u.id)
}

func (u *webauthnUser) WebAuthnName() string {
	return u.accountName
}

func (u *webauthnUser) WebAuthnDisplayName() string {
	return u.accountName
}

func (u *webauthnUser) WebAuthnCredentials() []wan.Credential {
	creds := make([]wan.Credential, len(u.records))
	for i, r := range u.records {
		creds[i] = r.credential
	}

	return creds
}

func (u *webauthnUser) mfaIDFor(credentialID []byte) string {
	for _, r := range u.records {
		if bytes.Equal(r.credential.ID, credentialID) {
			return r.mfaID
		}
	}

	return ""
}

func (s *Service) loadUser(ctx context.Context, userID, accountName string, scope mfa.Scope) (*webauthnUser, error) {
	creds, err := s.store.ListAccepted(ctx, userID, scope)
	if err != nil {
		return nil, fmt.Errorf("list mfa credentials: %w", err)
	}

	user := &webauthnUser{id: userID, accountName: accountName}
	for _, c := range creds {
		if c.Type != mfa.CredentialWebAuthn {
			continue
		}

		plaintext, err := s.keys.Decrypt(c.Credential)
		if err != nil {
			return nil, fmt.Errorf("decrypt webauthn credential %s: %w", c.ID, err)
		}

		var wc wan.Credential
		if err := json.Unmarshal(plaintext, &wc); err != nil {
			return nil, fmt.Errorf("unmarshal webauthn credential %s: %w", c.ID, err)
		}

		user.records = append(user.records, credentialRecord{mfaID: c.ID, credential: wc})
	}

	return user, nil
}

type ceremonySession struct {
	UserID   string          `json:"user_id"`
	TenantID string          `json:"tenant_id"`
	Data     wan.SessionData `json:"data"`
}

func regKey(ceremonyID string) string {
	return "webauthn:reg:" + ceremonyID
}

func authKey(ceremonyID string) string {
	return "webauthn:auth:" + ceremonyID
}

func (s *Service) storeSession(ctx context.Context, key, userID string, data wan.SessionData, tenantID string) error {
	blob, err := json.Marshal(ceremonySession{UserID: userID, TenantID: tenantID, Data: data})
	if err != nil {
		return fmt.Errorf("marshal webauthn ceremony session: %w", err)
	}

	if err := s.cache.SetWithTTL(ctx, key, string(blob), ceremonyTTL); err != nil {
		return fmt.Errorf("store webauthn ceremony session: %w", err)
	}

	return nil
}

func (s *Service) loadSession(ctx context.Context, key string) (*ceremonySession, error) {
	value, found, err := s.cache.Get(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("load webauthn ceremony session: %w", err)
	}

	if !found {
		return nil, ErrCeremonyExpired
	}

	var sess ceremonySession
	if err := json.Unmarshal([]byte(value), &sess); err != nil {
		return nil, fmt.Errorf("unmarshal webauthn ceremony session: %w", err)
	}

	return &sess, nil
}
