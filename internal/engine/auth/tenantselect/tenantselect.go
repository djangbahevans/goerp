// Package tenantselect holds the selection_token a tenantless sign-in
// answers with when the account belongs to several tenants
// (auth-internals.md §3 "Cross-tenant user membership"). The token stands
// in for the password just verified, so POST /auth/select-tenant can
// finish the sign-in for the chosen tenant without asking for it again.
// It lives briefly in Redis and works once.
package tenantselect

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"fmt"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/cache"
)

// TTL is how long a token stays usable.
const TTL = 120 * time.Second

const keyPrefix = "auth:tenant_select:"

// ErrInvalidToken reports a token that is unknown, expired or already used.
var ErrInvalidToken = errors.New("tenantselect: invalid token")

// Grant is what a token stands for: one user's verified password, and the
// sign-in choices that came with it.
type Grant struct {
	UserID   string `json:"user_id"`
	Remember bool   `json:"remember"`
	DeviceID string `json:"device_id,omitempty"`
}

type Store struct {
	cache *cache.Client
}

func NewStore(cacheClient *cache.Client) *Store {
	return &Store{cache: cacheClient}
}

// Issue stores g under a fresh token.
func (s *Store) Issue(ctx context.Context, g Grant) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate selection token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)

	data, err := json.Marshal(g)
	if err != nil {
		return "", fmt.Errorf("encode selection grant: %w", err)
	}
	if err := s.cache.SetWithTTL(ctx, keyPrefix+token, string(data), TTL); err != nil {
		return "", fmt.Errorf("store selection grant: %w", err)
	}
	return token, nil
}

// Consume returns the grant stored under token and deletes it, so a token
// works once. ErrInvalidToken for an unknown, expired or used token.
func (s *Store) Consume(ctx context.Context, token string) (*Grant, error) {
	if token == "" {
		return nil, ErrInvalidToken
	}
	data, found, err := s.cache.GetDel(ctx, keyPrefix+token)
	if err != nil {
		return nil, fmt.Errorf("consume selection token: %w", err)
	}
	if !found {
		return nil, ErrInvalidToken
	}
	var g Grant
	if err := json.Unmarshal([]byte(data), &g); err != nil {
		return nil, fmt.Errorf("decode selection grant: %w", err)
	}
	return &g, nil
}
