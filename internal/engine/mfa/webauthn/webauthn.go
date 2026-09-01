// Package webauthn implements WebAuthn/Passkey registration and login
// ceremonies — auth-internals.md §8's "WebAuthn / Passkeys" section — on
// top of mfa.Store (the user_mfa row store, goerp#296), rowcrypt
// (credential-at-rest encryption, goerp#297), and
// github.com/go-webauthn/webauthn (the library the doc's own
// BeginRegistration/FinishRegistration/BeginLogin/FinishLogin pseudocode
// names, the same reference-implementation-naming convention the TOTP
// ticket's doc pseudocode used for pquerna/otp).
//
// Like totp.Service and recoverycode.Service (goerp#300/#302), this
// package is Go-level only — no HTTP routes. A caller (a future ticket)
// is expected to expose Begin/Finish over HTTP, JSON-encoding the
// options this package returns and passing the client's raw JSON
// response bytes back in.
package webauthn

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	wan "github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/djangbahevans/goerp/internal/engine/auth/rowcrypt"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/mfa"
)

// Config is this package's own Relying Party configuration — per-subsystem
// env-var ownership, same convention cache/db/search/secrets/storage/
// temporal already use, not fields grown onto the central config.Config.
type Config struct {
	RPID          string   `env:"GOERP_WEBAUTHN_RP_ID,required"`
	RPDisplayName string   `env:"GOERP_WEBAUTHN_RP_DISPLAY_NAME" envDefault:"GoERP"`
	RPOrigins     []string `env:"GOERP_WEBAUTHN_RP_ORIGINS,required" envSeparator:","`
}

// ceremonyTTL matches auth-internals.md §8's own 5-minute window for both
// registration and login challenges.
const ceremonyTTL = 5 * time.Minute

var (
	// ErrCeremonyExpired covers both an unknown and an expired ceremony
	// ID — Redis TTL expiry and "never existed" look identical from this
	// package's side, and callers don't need to distinguish them.
	ErrCeremonyExpired = errors.New("webauthn ceremony expired or not found")
	// ErrCeremonyUserMismatch means ceremonyID exists but was started for
	// a different user — never trust the caller-supplied userID alone.
	ErrCeremonyUserMismatch = errors.New("webauthn ceremony does not belong to this user")
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
}

func NewService(cfg Config, store *mfa.Store, keys *rowcrypt.RowKeySet, cacheClient *cache.Client) (*Service, error) {
	webAuthn, err := wan.New(&wan.Config{
		RPID:          cfg.RPID,
		RPDisplayName: cfg.RPDisplayName,
		RPOrigins:     cfg.RPOrigins,
	})
	if err != nil {
		return nil, fmt.Errorf("configure webauthn relying party: %w", err)
	}
	return &Service{webAuthn: webAuthn, store: store, keys: keys, cache: cacheClient}, nil
}

// BeginRegistration starts a registration ceremony for userID, excluding
// their already-enrolled WebAuthn credentials (so re-registering the same
// authenticator is rejected client-side rather than silently duplicated).
// optionsJSON is the CredentialCreationOptions to serialize straight to
// the client; ceremonyID must be round-tripped back to FinishRegistration.
func (s *Service) BeginRegistration(ctx context.Context, userID, accountName string) (optionsJSON []byte, ceremonyID string, err error) {
	user, err := s.loadUser(ctx, userID, accountName)
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

	ceremonyID = uuid.NewString()
	if err := s.storeSession(ctx, regKey(ceremonyID), userID, *sessionData); err != nil {
		return nil, "", err
	}

	// encoding/json/v2 omits a zero-valued, omitempty-tagged struct field
	// (go-webauthn's own CredentialCreation.AuthenticatorSelection) that v1
	// always included as {} — a real difference, verified against the real
	// library, but an absent optional field and an empty object are
	// equivalent to any spec-compliant WebAuthn client.
	//
	// optionsJSON is eventually written straight to an HTTP response body
	// by a future caller, so it gets the same HTML/JS-escape parity as
	// every other client-facing encode call in this migration.
	optionsJSON, err = json.Marshal(creation, jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true))
	if err != nil {
		return nil, "", fmt.Errorf("marshal webauthn registration options: %w", err)
	}
	return optionsJSON, ceremonyID, nil
}

// FinishRegistration completes a registration ceremony started by
// BeginRegistration, verifying responseJSON (the client's raw
// CredentialCreationResponse JSON) against the stored ceremony session,
// then persisting the new credential as an encrypted system.user_mfa row.
// The ceremony session is deleted whether this succeeds or fails —
// single-use, per auth-internals.md §8.
func (s *Service) FinishRegistration(ctx context.Context, userID, ceremonyID, accountName string, responseJSON []byte, label *string) (*mfa.Credential, error) {
	key := regKey(ceremonyID)
	sess, sessErr := s.loadSession(ctx, key)
	defer func() { _ = s.cache.Delete(ctx, key) }()
	if sessErr != nil {
		return nil, sessErr
	}
	if sess.UserID != userID {
		return nil, ErrCeremonyUserMismatch
	}

	user, err := s.loadUser(ctx, userID, accountName)
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

	row, err := s.store.Insert(ctx, userID, mfa.CredentialWebAuthn, ciphertext, label)
	if err != nil {
		return nil, fmt.Errorf("store webauthn credential: %w", err)
	}
	return row, nil
}

// BeginLogin starts a login ceremony for userID against their enrolled
// WebAuthn credentials.
func (s *Service) BeginLogin(ctx context.Context, userID, accountName string) (optionsJSON []byte, ceremonyID string, err error) {
	user, err := s.loadUser(ctx, userID, accountName)
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

	ceremonyID = uuid.NewString()
	if err := s.storeSession(ctx, authKey(ceremonyID), userID, *sessionData); err != nil {
		return nil, "", err
	}

	// Same HTML/JS-escape parity as BeginRegistration's optionsJSON above.
	optionsJSON, err = json.Marshal(assertion, jsontext.EscapeForHTML(true), jsontext.EscapeForJS(true))
	if err != nil {
		return nil, "", fmt.Errorf("marshal webauthn login options: %w", err)
	}
	return optionsJSON, ceremonyID, nil
}

// FinishLogin completes a login ceremony started by BeginLogin, verifying
// responseJSON (the client's raw CredentialAssertionResponse JSON)
// against the stored ceremony session. On success it returns the matched
// credential's system.user_mfa row id, having already updated that row's
// sign count and last_used_at. On a detected clone/replay it revokes the
// matched credential and returns *CloneDetectedError instead of a
// credential id. The ceremony session is deleted whether this succeeds or
// fails — single-use, per auth-internals.md §8.
func (s *Service) FinishLogin(ctx context.Context, userID, ceremonyID, accountName string, responseJSON []byte) (credentialID string, err error) {
	key := authKey(ceremonyID)
	sess, sessErr := s.loadSession(ctx, key)
	defer func() { _ = s.cache.Delete(ctx, key) }()
	if sessErr != nil {
		return "", sessErr
	}
	if sess.UserID != userID {
		return "", ErrCeremonyUserMismatch
	}

	user, err := s.loadUser(ctx, userID, accountName)
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
		// auth-internals.md §8 calls emitAuditEvent("mfa.clone_suspected",
		// user.ID, credential.ID) here. system.auth_audit_log (the doc's
		// §17 audit trail table) doesn't exist yet — nexus-docs backlog
		// #298, unfiled — so this logs structurally instead of writing a
		// real audit row. Swap this for a real emit once that table
		// lands; the revoke above is the actual security control and
		// doesn't depend on it.
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

// encryptCredential's own JSON never leaves this process unencrypted —
// unlike optionsJSON above, it's decrypted and Unmarshaled only by
// loadUser, so it doesn't need the HTML/JS-escape parity a client-facing
// encode does.
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

// webauthnUser adapts one goerp user to the library's User interface.
type webauthnUser struct {
	id          string
	accountName string
	records     []credentialRecord
}

func (u *webauthnUser) WebAuthnID() []byte          { return []byte(u.id) }
func (u *webauthnUser) WebAuthnName() string        { return u.accountName }
func (u *webauthnUser) WebAuthnDisplayName() string { return u.accountName }
func (u *webauthnUser) WebAuthnCredentials() []wan.Credential {
	creds := make([]wan.Credential, len(u.records))
	for i, r := range u.records {
		creds[i] = r.credential
	}
	return creds
}

// mfaIDFor returns the system.user_mfa row id for the credential whose ID
// matches credentialID, or "" if none of this user's loaded records match.
func (u *webauthnUser) mfaIDFor(credentialID []byte) string {
	for _, r := range u.records {
		if bytes.Equal(r.credential.ID, credentialID) {
			return r.mfaID
		}
	}
	return ""
}

// loadUser fetches userID's active WebAuthn credentials, decrypting and
// unmarshaling each into a wan.Credential.
func (s *Service) loadUser(ctx context.Context, userID, accountName string) (*webauthnUser, error) {
	creds, err := s.store.ListActiveByUser(ctx, userID)
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

// ceremonySession is what's actually stored in Redis under a ceremony
// key — the session data the library needs to Finish the ceremony, plus
// the user_id it was started for (so FinishRegistration/FinishLogin can
// reject a ceremonyID being replayed against a different user).
type ceremonySession struct {
	UserID string          `json:"user_id"`
	Data   wan.SessionData `json:"data"`
}

func regKey(ceremonyID string) string  { return "webauthn:reg:" + ceremonyID }
func authKey(ceremonyID string) string { return "webauthn:auth:" + ceremonyID }

// Same reasoning as encryptCredential: this JSON only round-trips through
// Redis back to loadSession, never to a client, so it skips the
// HTML/JS-escape parity optionsJSON needs.
func (s *Service) storeSession(ctx context.Context, key, userID string, data wan.SessionData) error {
	blob, err := json.Marshal(ceremonySession{UserID: userID, Data: data})
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
