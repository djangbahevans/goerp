package permcache

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/stdlib"
	"github.com/rs/zerolog/log"

	"github.com/djangbahevans/goerp/internal/engine/permission"
	"github.com/djangbahevans/goerp/internal/engine/role"
	"github.com/djangbahevans/goerp/internal/engine/tenant"
)

// RolesChangedChannel is the Postgres NOTIFY channel a tenant's role or
// role-permission change is announced on; the payload is the tenant slug.
const RolesChangedChannel = "roles_changed"

const listenerReconnectBackoff = 2 * time.Second

// NotifyRolesChanged queues the announcement inside tx, so replicas hear it
// only if the change commits.
func NotifyRolesChanged(ctx context.Context, tx *sql.Tx, tenantSlug string) error {
	if _, err := tx.ExecContext(ctx, `SELECT pg_notify($1, $2)`, RolesChangedChannel, tenantSlug); err != nil {
		return fmt.Errorf("notify roles changed: %w", err)
	}
	return nil
}

// Listener keeps this replica's RolePermissionMap current with role changes
// made on any replica: every RolesChangedChannel notification rebuilds that
// tenant's entries. It follows tenantconfig.Listener: a dedicated LISTEN
// connection, reconnecting after listenerReconnectBackoff.
type Listener struct {
	db       *sql.DB
	tenants  *tenant.Store
	roles    *role.Store
	registry func() *permission.PermissionRegistry
	perms    *RolePermissionMap

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewListener(db *sql.DB, tenants *tenant.Store, roles *role.Store, registry func() *permission.PermissionRegistry, perms *RolePermissionMap) *Listener {
	return &Listener{db: db, tenants: tenants, roles: roles, registry: registry, perms: perms}
}

// Start runs Run in the background, reconnecting on error, until Stop or
// ctx is done. onReady is passed to every Run attempt.
func (l *Listener) Start(ctx context.Context, onReady func()) {
	ctx, cancel := context.WithCancel(ctx)
	l.cancel = cancel
	l.wg.Go(func() {
		for {
			if err := l.Run(ctx, onReady); err != nil {
				log.Warn().Err(err).Msg("permcache: roles listener error, reconnecting")
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(listenerReconnectBackoff):
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

// Run blocks, rebuilding the named tenant for each notification, until ctx
// is cancelled (nil) or the connection is lost (an error). Once LISTEN is in
// place it resyncs every tenant, catching up on anything announced while no
// LISTEN was active (before the first Run, or during a reconnect), and then
// calls onReady if set.
func (l *Listener) Run(ctx context.Context, onReady func()) error {
	conn, err := l.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire dedicated connection for LISTEN: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, "LISTEN "+RolesChangedChannel); err != nil {
		return fmt.Errorf("listen on %s: %w", RolesChangedChannel, err)
	}
	if err := l.perms.Resync(ctx, l.tenants, l.roles, l.registry); err != nil {
		return fmt.Errorf("resync role permission map: %w", err)
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
			if err := l.perms.RebuildTenant(ctx, l.roles, l.registry, notification.Payload); err != nil {
				log.Warn().Err(err).Str("tenant", notification.Payload).Msg("permcache: rebuild after roles_changed failed")
			}
		}
	})
	if err != nil && ctx.Err() != nil {
		return nil
	}
	return err
}
