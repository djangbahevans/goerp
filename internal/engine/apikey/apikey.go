// Package apikey manages API key generation, hashing, lookup and revocation. Request
// authentication and scope enforcement belong to the auth middleware.
package apikey

import (
	"errors"
	"time"
)

var ErrAPIKeyNotFound = errors.New("api key not found")

// APIKey is one system.api_keys row.
type APIKey struct {
	ID           string     `json:"id"`
	TenantID     string     `json:"tenant_id"`
	UserID       *string    `json:"user_id,omitempty"` // nil = service key
	Name         string     `json:"name"`
	KeyHash      string     `json:"key_hash"`
	KeyPrefix    string     `json:"key_prefix"`
	Scopes       []string   `json:"scopes"`
	AllowedIPs   []string   `json:"allowed_ips,omitempty"` // nil = any IP allowed
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	LastUsedAt   *time.Time `json:"last_used_at,omitempty"`
	LastUsedIP   *string    `json:"last_used_ip,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	CreatedBy    *string    `json:"created_by,omitempty"`
	RevokedAt    *time.Time `json:"revoked_at,omitempty"`
	RevokeReason *string    `json:"revoke_reason,omitempty"`
}
