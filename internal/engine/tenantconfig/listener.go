package tenantconfig

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/rs/zerolog/log"
)

// reconnectBackoff is how long Start waits before retrying Run after it
// returns a real error (the dedicated connection was lost, or never
// acquired in the first place) — long enough not to hammer a genuinely
// down Postgres, short enough that recovery is still fast once it's back.
const reconnectBackoff = 2 * time.Second

// Listener subscribes to configChangedChannel on a dedicated connection
// and invalidates the matching entry in resolver's own in-memory cache for
// every notification it receives — Store.Set's cross-instance counterpart
// to Resolver's own local, TTL-only eviction.
type Listener struct {
	db       *sql.DB
	resolver *Resolver

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewListener(db *sql.DB, resolver *Resolver) *Listener {
	return &Listener{db: db, resolver: resolver}
}

// Start launches Run in a background goroutine, retrying after
// reconnectBackoff on any error, until Stop is called or ctx is done —
// self-healing against a transient connection loss without engine
// startup ever blocking on it. Cross-instance invalidation is a latency
// optimization on top of cacheTTL's own correctness backstop, not
// something engine startup can fail hard on the way Stage 1's primary
// Postgres/Redis checks do (engine-internals.md §2) — a Listener that
// can't yet connect just leaves every cache entry to expire on its own
// TTL until Start's retries catch up. onReady is passed through to every
// Run attempt (see Run's own doc comment) — most callers pass nil, since
// Start's whole point is not to make anyone wait on it; a caller that
// does need to know a LISTEN is actually in place (a test asserting
// convergence, most likely) can still observe it.
func (l *Listener) Start(ctx context.Context, onReady func()) {
	ctx, cancel := context.WithCancel(ctx)
	l.cancel = cancel
	l.wg.Go(func() {
		for {
			if err := l.Run(ctx, onReady); err != nil {
				log.Warn().Err(err).Msg("tenantconfig: listener error, reconnecting")
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(reconnectBackoff):
			}
		}
	})
}

// Stop cancels the goroutine Start launched and waits for it to exit.
func (l *Listener) Stop() {
	if l.cancel != nil {
		l.cancel()
	}
	l.wg.Wait()
}

// Run blocks, LISTENing on configChangedChannel and invalidating
// resolver's cache for every notification received, until ctx is
// cancelled (a clean return) or the dedicated connection is lost (a
// returned error — the caller decides whether to reconnect by calling
// Run again). onReady, if non-nil, is called once LISTEN has actually
// been issued — a caller that needs to know when notifications sent from
// this point on are guaranteed to be received (orderly startup sequencing,
// or a test asserting cross-instance convergence) waits on it rather than
// racing Run's own goroutine.
func (l *Listener) Run(ctx context.Context, onReady func()) error {
	conn, err := l.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire dedicated connection for LISTEN: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "LISTEN "+configChangedChannel); err != nil {
		return fmt.Errorf("listen on %s: %w", configChangedChannel, err)
	}
	if onReady != nil {
		onReady()
	}

	err = conn.Raw(func(driverConn any) error {
		pgConn := driverConn.(*stdlib.Conn).Conn()
		for {
			notification, err := pgConn.WaitForNotification(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return fmt.Errorf("wait for notification: %w", err)
			}
			l.handleNotification(notification.Payload)
		}
	})
	if err != nil && ctx.Err() != nil {
		return nil
	}
	return err
}

// handleNotification invalidates the (tenant, key) pair payload names.
// A payload that fails to decode is logged and dropped — one malformed
// notification (which nothing in this codebase ever legitimately
// produces, since Store.Set is configChangedChannel's only publisher)
// must not take down the whole listen loop.
func (l *Listener) handleNotification(payload string) {
	var decoded configChangedPayload
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		log.Warn().Err(err).Str("payload", payload).Msg("tenantconfig: malformed config_changed notification, dropping")
		return
	}
	l.resolver.Invalidate(decoded.TenantID, decoded.Key)
}
