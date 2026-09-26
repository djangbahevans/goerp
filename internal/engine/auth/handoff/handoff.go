// Package handoff carries a browser sign-in from the shared-domain login
// host to the tenant's own host (auth-internals.md §3 "Shared-domain
// handoff"). Session cookies are host-only and every Class A route
// resolves its tenant from the Host header, so a session issued on a host
// that resolves to no tenant is unusable. A sign-in there stores a
// single-use Grant under a random code instead, and the tenant's host
// exchanges the code for the session at POST /auth/handoff.
package handoff

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/auth/loginsession"
	"github.com/djangbahevans/goerp/internal/engine/cache"
	tenantresolve "github.com/djangbahevans/goerp/internal/engine/tenant/resolve"
)

// TTL is how long a code stays exchangeable.
const TTL = 60 * time.Second

const keyPrefix = "auth:handoff:"

// ErrInvalidCode reports a code that is unknown, expired or already used.
var ErrInvalidCode = errors.New("handoff: invalid code")

// Grant is what a code stands for: a password verified moments ago for
// one user signing in to one tenant.
type Grant struct {
	UserID                    string `json:"user_id"`
	TenantID                  string `json:"tenant_id"`
	Remember                  bool   `json:"remember"`
	PasswordUpdateRecommended bool   `json:"password_update_recommended"`
}

// Response is the handoff body a sign-in on another host answers with.
type Response struct {
	Host string `json:"host"`
	Code string `json:"code"`
}

type Store struct {
	cache          *cache.Client
	resolver       *tenantresolve.Resolver
	platformDomain string
}

func NewStore(cacheClient *cache.Client, resolver *tenantresolve.Resolver, platformDomain string) *Store {
	return &Store{cache: cacheClient, resolver: resolver, platformDomain: platformDomain}
}

// Needed reports whether a sign-in to tenantID from r must hand off
// rather than set cookies: a web client on a host that doesn't resolve to
// that tenant. A non-browser client holds no cookies, so never needs one.
func (s *Store) Needed(ctx context.Context, r *http.Request, tenantID string) bool {
	if loginsession.IsNonBrowser(r) {
		return false
	}
	tc, err := s.resolver.ResolveByHost(ctx, r.Host)
	return err != nil || tc.TenantID != tenantID
}

// Issue stores g under a fresh code and returns the response pointing the
// browser at the tenant's default domain.
func (s *Store) Issue(ctx context.Context, g Grant, tenantSlug string) (Response, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return Response{}, fmt.Errorf("generate handoff code: %w", err)
	}
	code := base64.RawURLEncoding.EncodeToString(raw)

	data, err := json.Marshal(g)
	if err != nil {
		return Response{}, fmt.Errorf("encode handoff grant: %w", err)
	}
	if err := s.cache.SetWithTTL(ctx, keyPrefix+code, string(data), TTL); err != nil {
		return Response{}, fmt.Errorf("store handoff grant: %w", err)
	}
	return Response{Host: tenantSlug + "." + s.platformDomain, Code: code}, nil
}

// Consume returns the grant stored under code and deletes it, so a code
// works once. ErrInvalidCode for an unknown, expired or used code.
func (s *Store) Consume(ctx context.Context, code string) (*Grant, error) {
	if code == "" {
		return nil, ErrInvalidCode
	}
	data, found, err := s.cache.GetDel(ctx, keyPrefix+code)
	if err != nil {
		return nil, fmt.Errorf("consume handoff code: %w", err)
	}
	if !found {
		return nil, ErrInvalidCode
	}
	var g Grant
	if err := json.Unmarshal([]byte(data), &g); err != nil {
		return nil, fmt.Errorf("decode handoff grant: %w", err)
	}
	return &g, nil
}
