package engine

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/rs/zerolog/log"
)

// rateLimitMiddleware implements engine-internals.md §6 step 4 — a
// Redis-backed sliding-window limiter applied to every request, using the
// resolved route's own RouteManifest.RateLimit when it declares one, or
// defaultCfg otherwise (never both, matching go-sdk-reference.md §2a's
// own "nil means use the engine-wide default, not no limit").
//
// This middleware sits before tenant resolution and auth in the
// documented chain order (§6's own buildChain pseudocode: rate limiting
// is step 4, tenant resolution step 5, token validation steps 6-8) — so
// unlike its position might suggest, a route declaring
// RateLimitConfig.Scope "user"/"tenant"/"api_key" has no resolved
// AuthContext/TenantContext to partition by yet at this point. Rather
// than peek at an unverified token's claims just to pick a bucket (which
// would let a caller choose which bucket to attack) or silently skip the
// limit for those scopes, this middleware falls back to IP-based
// partitioning for all four scope values — strictly more conservative
// (never a coarser bucket shared by more callers than the declared scope
// intended, only ever a finer one: real IP), never less protection than
// what was declared. go-sdk-reference.md's own scope doc already singles
// out "ip" as "the only scope meaningful for engine.AuthNone routes" —
// the same reasoning extends to every route at this specific point in
// the pipeline, authenticated or not.
func rateLimitMiddleware(redisClient *cache.Client, defaultCfg route.RateLimitConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rr := routeResolutionFromContext(r.Context())

			cfg := defaultCfg
			bucket := "default"
			if rr != nil && rr.entry.Manifest.RateLimit != nil {
				cfg = *rr.entry.Manifest.RateLimit
				bucket = "route:" + rr.entry.PathTemplate
			}
			if cfg.Requests <= 0 || cfg.WindowSeconds <= 0 {
				// A misconfigured/zero-value limit fails open rather than
				// blocking every request against it — the same posture
				// this middleware takes for a Redis error below.
				next.ServeHTTP(w, r)
				return
			}

			// realIPMiddleware, earlier in the chain, already normalizes
			// r.RemoteAddr to a bare IP (no port) — same convention
			// authcheck.Checker.ipAllowed's own remoteIP parameter relies
			// on.
			key := fmt.Sprintf("ratelimit:%s:ip:%s", bucket, r.RemoteAddr)

			allowed, retryAfter, err := redisClient.SlidingWindowAllow(r.Context(), key, cfg.Requests, time.Duration(cfg.WindowSeconds)*time.Second)
			if err != nil {
				log.Warn().Err(err).Str("key", key).Msg("engine: rate limit check failed, failing open")
				next.ServeHTTP(w, r)
				return
			}
			if !allowed {
				w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retryAfter.Seconds()))))
				writeRouteError(w, http.StatusTooManyRequests, "rate_limit_exceeded", "too many requests")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
