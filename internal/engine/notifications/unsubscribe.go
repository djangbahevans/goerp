package notifications

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/auth/signingkey"
	"github.com/golang-jwt/jwt/v5"
)

// UnsubscribeTokenTTL is how long a one-click unsubscribe link keeps
// working after its email is sent.
const UnsubscribeTokenTTL = 365 * 24 * time.Hour

// unsubscribeIssuer differs from the "goerp" issuer access tokens carry,
// and authcheck requires, so an unsubscribe token signed with the same
// key is never accepted as a bearer token.
const (
	unsubscribeIssuer  = "goerp:notif-unsubscribe"
	unsubscribePurpose = "notif_unsubscribe"
)

// ErrInvalidUnsubscribeToken covers every way an unsubscribe token can
// fail to verify: malformed, wrong signature or key, expired, or not an
// unsubscribe token.
var ErrInvalidUnsubscribeToken = errors.New("invalid or expired unsubscribe token")

// UnsubscribeClaims is the unsubscribe token's claim set: sub is the
// user, tid their tenant, ntype the notification type whose email they
// turn off.
type UnsubscribeClaims struct {
	jwt.RegisteredClaims
	TenantID         string `json:"tid"`
	NotificationType string `json:"ntype"`
	Purpose          string `json:"purpose"`
}

// UnsubscribeCodec mints and verifies the signed tokens in notification
// emails' {{.UnsubscribeURL}} (notification-system.md §10), with the
// engine's JWT signing keys.
type UnsubscribeCodec struct {
	keys *signingkey.SigningKeySet
}

func NewUnsubscribeCodec(keys *signingkey.SigningKeySet) *UnsubscribeCodec {
	return &UnsubscribeCodec{keys: keys}
}

// Issue mints a token that turns off userID's email for notificationType
// in tenantID.
func (c *UnsubscribeCodec) Issue(userID, tenantID, notificationType string) (string, error) {
	now := time.Now()
	claims := UnsubscribeClaims{
		Issuer:           unsubscribeIssuer,
		Subject:          userID,
		IssuedAt:         jwt.NewNumericDate(now),
		ExpiresAt:        jwt.NewNumericDate(now.Add(UnsubscribeTokenTTL)),
		TenantID:         tenantID,
		NotificationType: notificationType,
		Purpose:          unsubscribePurpose,
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = c.keys.Active.KID
	signed, err := tok.SignedString(c.keys.Active.Private)
	if err != nil {
		return "", fmt.Errorf("sign unsubscribe token: %w", err)
	}
	return signed, nil
}

// Verify checks raw's signature against the active or a previous signing
// key, its expiry, issuer and purpose, and returns its claims.
func (c *UnsubscribeCodec) Verify(raw string) (*UnsubscribeClaims, error) {
	claims := &UnsubscribeClaims{}
	_, err := jwt.ParseWithClaims(raw, claims, c.keyFunc,
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithIssuer(unsubscribeIssuer),
		jwt.WithExpirationRequired())
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidUnsubscribeToken, err)
	}
	if claims.Purpose != unsubscribePurpose || claims.Subject == "" || claims.TenantID == "" || claims.NotificationType == "" {
		return nil, ErrInvalidUnsubscribeToken
	}
	return claims, nil
}

func (c *UnsubscribeCodec) keyFunc(token *jwt.Token) (any, error) {
	kid, _ := token.Header["kid"].(string)
	if kid == c.keys.Active.KID {
		return c.keys.Active.Public, nil
	}
	for _, k := range c.keys.Previous {
		if kid == k.KID {
			return k.Public, nil
		}
	}
	return nil, fmt.Errorf("unrecognized signing key kid %v", token.Header["kid"])
}

// Unsubscribe turns off userID's email for notificationType, creating the
// type's row if needed, as a PATCH of { types: { notificationType: {
// email: false } } } would.
func (s *Store) Unsubscribe(ctx context.Context, tenantSlug, tenantID, userID, notificationType string) error {
	return s.UpdatePreferences(ctx, tenantSlug, tenantID, userID, nil, map[string]ChannelsPatch{
		notificationType: {Email: new(false)},
	})
}
