package engine

import (
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/djangbahevans/goerp/internal/engine/cache"
	"github.com/djangbahevans/goerp/internal/engine/httperr"
	"github.com/djangbahevans/goerp/internal/engine/route"
	"github.com/rs/zerolog/log"
)

// Rate limits run before tenant/auth resolution, so every declared scope uses verified
// client IP partitioning. Route limits replace the engine default.
func rateLimitMiddleware(redisClient *cache.Client, defaultCfg route.RateLimitConfig) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rr := routeResolutionFromContext(r.Context())
			if rr != nil && rr.entry.Manifest.OwnRateLimit {
				next.ServeHTTP(w, r)
				return
			}

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

			key := fmt.Sprintf("ratelimit:%s:ip:%s", bucket, r.RemoteAddr)

			allowed, retryAfter, err := redisClient.SlidingWindowAllow(r.Context(), key, cfg.Requests, time.Duration(cfg.WindowSeconds)*time.Second)
			if err != nil {
				log.Warn().Err(err).Str("key", key).Msg("engine: rate limit check failed, failing open")
				next.ServeHTTP(w, r)
				return
			}
			if !allowed {
				w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retryAfter.Seconds()))))
				httperr.Write(r.Context(), w, http.StatusTooManyRequests, "rate_limit_exceeded", "too many requests")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
