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
	"net/url"
	"slices"
	"strings"
	"time"
	"uuid"

	"github.com/djangbahevans/goerp/internal/engine/authaudit"
	"github.com/go-webauthn/webauthn/protocol"
	wan "github.com/go-webauthn/webauthn/webauthn"

	"github.com/djangbahevans/goerp/internal/engine/auth/rowcrypt"
	"github.com/djangbahevans/goerp/internal/engine/auth/session"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/mfa"
)

type Config struct {
	RPID          string
	RPDisplayName string
	RPOrigins     []string
	BaseURL       string
}

const ceremonyTTL = 5 * time.Minute

var (
	ErrCeremonyExpired       = errors.New("webauthn ceremony expired or not found")
	ErrCeremonyUserMismatch  = errors.New("webauthn ceremony binding mismatch")
	ErrNoEnrolledCredentials = errors.New("user has no enrolled webauthn credentials")
	ErrInvalidResponse       = errors.New("invalid webauthn response")
	ErrInvalidOrigin         = errors.New("invalid webauthn origin")
)

type CloneDetectedError struct {
	CredentialID string
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
	cfg      Config
	binding  string
}

func NewService(cfg Config, store *mfa.Store, keys *rowcrypt.RowKeySet, cacheClient *cache.Client, sessions *session.Store) (*Service, error) {
	if cfg.RPDisplayName == "" {
		cfg.RPDisplayName = "GoERP"
	}

	if cfg.RPID == "" {
		return nil, errors.New("webauthn RP ID is required")
	}

	if cfg.BaseURL != "" {
		base, err := url.Parse(cfg.BaseURL)
		if err != nil {
			return nil, fmt.Errorf("configure webauthn base URL: %w", err)
		}

		base.Path, base.RawPath, base.RawQuery, base.Fragment = "", "", "", ""
		cfg.BaseURL = base.String()
		if _, err := parseOrigin(cfg.BaseURL); err != nil {
			return nil, fmt.Errorf("configure webauthn base URL: %w", err)
		}
	}

	for _, origin := range cfg.RPOrigins {
		if _, err := parseOrigin(origin); err != nil {
			return nil, fmt.Errorf("configure webauthn origin: %w", err)
		}
	}

	origins := cfg.RPOrigins
	if len(origins) == 0 && cfg.BaseURL != "" {
		origins = []string{cfg.BaseURL}
	}

	webAuthn, err := wan.New(&wan.Config{
		RPID:          cfg.RPID,
		RPDisplayName: cfg.RPDisplayName,
		RPOrigins:     origins,
	})
	if err != nil {
		return nil, fmt.Errorf("configure webauthn relying party: %w", err)
	}

	return &Service{webAuthn: webAuthn, store: store, keys: keys, cache: cacheClient, sessions: sessions, cfg: cfg}, nil
}

func parseOrigin(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || strings.Contains(raw, "*") {
		return nil, ErrInvalidOrigin
	}

	local := u.Hostname() == "localhost" || strings.HasSuffix(u.Hostname(), ".localhost")
	if u.Scheme != "https" && (u.Scheme != "http" || !local) {
		return nil, ErrInvalidOrigin
	}

	return u, nil
}

// ForRequest requires a host already resolved to the authenticated tenant.
func (s *Service) ForRequest(host, origin, binding string) (*Service, error) {
	u, err := parseOrigin(origin)
	requestHost, hostErr := url.Parse("https://" + host)
	if err != nil || hostErr != nil || !strings.EqualFold(requestHost.Hostname(), u.Hostname()) {
		return nil, ErrInvalidOrigin
	}

	if len(s.cfg.RPOrigins) > 0 {
		if !slices.Contains(s.cfg.RPOrigins, origin) {
			return nil, ErrInvalidOrigin
		}
	} else {
		base, err := parseOrigin(s.cfg.BaseURL)
		if err != nil || u.Scheme != base.Scheme || u.Port() != base.Port() {
			return nil, ErrInvalidOrigin
		}
	}

	cfg := s.cfg
	if u.Hostname() != cfg.RPID && !strings.HasSuffix(u.Hostname(), "."+cfg.RPID) {
		cfg.RPID = u.Hostname()
	}

	cfg.RPOrigins = []string{origin}
	bound, err := NewService(cfg, s.store, s.keys, s.cache, s.sessions)
	if err != nil {
		return nil, err
	}

	bound.binding = binding
	return bound, nil
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

	ceremonyID = uuid.NewV7().String()
	if err := s.storeSession(ctx, regKey(ceremonyID), userID, *sessionData, scope.TenantID); err != nil {
		return nil, "", err
	}

	optionsJSON, err = json.Marshal(creation, jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true))
	if err != nil {
		return nil, "", fmt.Errorf("marshal webauthn registration options: %w", err)
	}

	return optionsJSON, ceremonyID, nil
}

type Registration struct {
	Ciphertext []byte
}

func (s *Service) CheckRegistration(ctx context.Context, userID, ceremonyID, accountName string, responseJSON []byte, scope mfa.Scope) (*Registration, error) {
	sess, err := s.loadSession(ctx, regKey(ceremonyID))
	if err != nil {
		return nil, err
	}

	if !s.matches(sess, userID, scope) {
		return nil, ErrCeremonyUserMismatch
	}

	user, err := s.loadUser(ctx, userID, accountName, scope)
	if err != nil {
		return nil, err
	}

	parsed, err := protocol.ParseCredentialCreationResponseBytes(responseJSON)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidResponse, err)
	}

	credential, err := s.webAuthn.CreateCredential(user, sess.Data, parsed)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidResponse, err)
	}

	ciphertext, err := s.encryptCredential(credential)
	if err != nil {
		return nil, err
	}

	return &Registration{Ciphertext: ciphertext}, nil
}

func (s *Service) InsertRegistrationTx(ctx context.Context, tx *sql.Tx, registration *Registration, userID string, factorTenant *string, label *string) (*mfa.Credential, error) {
	return s.store.InsertScopedTx(ctx, tx, userID, factorTenant, mfa.CredentialWebAuthn, registration.Ciphertext, label)
}

func (s *Service) FinishRegistration(ctx context.Context, userID, ceremonyID, accountName string, responseJSON []byte, label *string, scope mfa.Scope, sessionID string) (*mfa.Credential, error) {
	registration, err := s.CheckRegistration(ctx, userID, ceremonyID, accountName, responseJSON, scope)
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

		row, err = s.InsertRegistrationTx(ctx, tx, registration, userID, factorTenant, label)
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
	return row, err
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

	ceremonyID = uuid.NewV7().String()
	if err := s.storeSession(ctx, authKey(ceremonyID), userID, *sessionData, scope.TenantID); err != nil {
		return nil, "", err
	}

	optionsJSON, err = json.Marshal(assertion, jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true))
	if err != nil {
		return nil, "", fmt.Errorf("marshal webauthn login options: %w", err)
	}

	return optionsJSON, ceremonyID, nil
}

// FinishLogin commits credential revocation even when clone detection fails authentication.
func (s *Service) FinishLogin(ctx context.Context, userID, ceremonyID, accountName string, responseJSON []byte, scope mfa.Scope) (string, error) {
	var id string
	var clone *CloneDetectedError
	err := s.store.WithTx(ctx, func(tx *sql.Tx) error {
		if err := s.store.LockUserTx(ctx, tx, userID); err != nil {
			return err
		}

		var err error
		id, err = s.FinishLoginTx(ctx, tx, userID, ceremonyID, accountName, responseJSON, scope, nil, authaudit.Row{})
		if c, ok := errors.AsType[*CloneDetectedError](err); ok {
			clone = c
			return nil
		}

		return err
	})
	if err != nil {
		return "", err
	}

	if clone != nil {
		return "", clone
	}

	return id, nil
}

// FinishLoginTx requires the account's MFA lock. The caller must commit on
// CloneDetectedError to persist the credential revocation and audit row.
func (s *Service) FinishLoginTx(ctx context.Context, tx *sql.Tx, userID, ceremonyID, accountName string, responseJSON []byte, scope mfa.Scope, audit *authaudit.Store, row authaudit.Row) (string, error) {
	sess, err := s.loadSession(ctx, authKey(ceremonyID))
	if err != nil {
		return "", err
	}

	if !s.matches(sess, userID, scope) {
		return "", ErrCeremonyUserMismatch
	}

	user, err := s.loadUserTx(ctx, tx, userID, accountName, scope)
	if err != nil {
		return "", err
	}

	parsed, err := protocol.ParseCredentialRequestResponseBytes(responseJSON)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidResponse, err)
	}

	matched, err := s.webAuthn.ValidateLogin(user, sess.Data, parsed)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidResponse, err)
	}

	mfaID := user.mfaIDFor(matched.ID)
	if mfaID == "" {
		return "", fmt.Errorf("validated credential %x matched no stored user_mfa row", matched.ID)
	}

	if matched.Authenticator.CloneWarning {
		if err := s.store.RevokeTx(ctx, tx, userID, mfaID); err != nil {
			return "", err
		}

		if audit != nil {
			row.EventType = "mfa.clone_suspected"
			row.UserID = userID
			row.TenantID = scope.TenantID
			row.Success = false
			row.FailureReason = "clone_suspected"
			row.Metadata, err = json.Marshal(map[string]string{"credential_id": mfaID})
			if err != nil {
				return "", err
			}

			if err := audit.InsertTx(ctx, tx, row); err != nil {
				return "", err
			}
		}

		return "", &CloneDetectedError{CredentialID: mfaID}
	}

	ciphertext, err := s.encryptCredential(matched)
	if err != nil {
		return "", err
	}

	if _, err := tx.ExecContext(ctx, `UPDATE system.user_mfa SET credential=$2, last_used_at=NOW() WHERE id=$1 AND revoked_at IS NULL`, mfaID, ciphertext); err != nil {
		return "", fmt.Errorf("update webauthn credential after login: %w", err)
	}

	return mfaID, nil
}

func (s *Service) encryptCredential(credential *wan.Credential) ([]byte, error) {
	blob, err := json.Marshal(storedCredential{Credential: *credential, RPID: s.webAuthn.Config.RPID})
	if err != nil {
		return nil, fmt.Errorf("marshal webauthn credential: %w", err)
	}

	ciphertext, err := s.keys.Encrypt(blob)
	if err != nil {
		return nil, fmt.Errorf("encrypt webauthn credential: %w", err)
	}

	return ciphertext, nil
}

type storedCredential struct {
	Credential wan.Credential `json:"credential"`
	RPID       string         `json:"rp_id"`
}

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
		return nil, err
	}

	return s.decodeUser(userID, accountName, creds)
}

func (s *Service) loadUserTx(ctx context.Context, tx *sql.Tx, userID, accountName string, scope mfa.Scope) (*webauthnUser, error) {
	creds, err := s.store.ListAcceptedTx(ctx, tx, userID, scope)
	if err != nil {
		return nil, err
	}

	return s.decodeUser(userID, accountName, creds)
}

func (s *Service) decodeUser(userID, accountName string, creds []*mfa.Credential) (*webauthnUser, error) {
	user := &webauthnUser{id: userID, accountName: accountName}
	for _, c := range creds {
		if c.Type != mfa.CredentialWebAuthn {
			continue
		}

		plaintext, err := s.keys.Decrypt(c.Credential)
		if err != nil {
			return nil, fmt.Errorf("decrypt webauthn credential %s: %w", c.ID, err)
		}

		var wc storedCredential
		if err := json.Unmarshal(plaintext, &wc); err != nil {
			return nil, fmt.Errorf("unmarshal webauthn credential %s: %w", c.ID, err)
		}

		if wc.RPID == s.webAuthn.Config.RPID {
			user.records = append(user.records, credentialRecord{mfaID: c.ID, credential: wc.Credential})
		}
	}

	return user, nil
}

type ceremonySession struct {
	UserID   string          `json:"user_id"`
	TenantID string          `json:"tenant_id"`
	Data     wan.SessionData `json:"data"`
	Origin   string          `json:"origin"`
	RPID     string          `json:"rp_id"`
	Binding  string          `json:"binding"`
}

func regKey(ceremonyID string) string {
	return "webauthn:reg:" + ceremonyID
}

func authKey(ceremonyID string) string {
	return "webauthn:auth:" + ceremonyID
}

func (s *Service) storeSession(ctx context.Context, key, userID string, data wan.SessionData, tenantID string) error {
	blob, err := json.Marshal(ceremonySession{UserID: userID, TenantID: tenantID, Data: data, Origin: s.webAuthn.Config.RPOrigins[0], RPID: s.webAuthn.Config.RPID, Binding: s.binding})
	if err != nil {
		return fmt.Errorf("marshal webauthn ceremony session: %w", err)
	}

	if err := s.cache.SetWithTTL(ctx, key, string(blob), ceremonyTTL); err != nil {
		return fmt.Errorf("store webauthn ceremony session: %w", err)
	}

	return nil
}

func (s *Service) loadSession(ctx context.Context, key string) (*ceremonySession, error) {
	value, found, err := s.cache.GetDel(ctx, key)
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

func (s *Service) matches(sess *ceremonySession, userID string, scope mfa.Scope) bool {
	return sess.UserID == userID && sess.TenantID == scope.TenantID && sess.RPID == s.webAuthn.Config.RPID && sess.Origin == s.webAuthn.Config.RPOrigins[0] && sess.Binding == s.binding
}

func InvalidAssertion(err error) bool {
	if _, ok := errors.AsType[*CloneDetectedError](err); ok {
		return true
	}

	return errors.Is(err, ErrInvalidResponse) || errors.Is(err, ErrCeremonyExpired) || errors.Is(err, ErrCeremonyUserMismatch) || errors.Is(err, ErrNoEnrolledCredentials)
}
