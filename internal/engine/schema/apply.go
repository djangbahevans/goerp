package schema

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"ariga.io/atlas/sql/migrate"
	"ariga.io/atlas/sql/postgres"
	"ariga.io/atlas/sql/schema"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rs/zerolog/log"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

// Execute applies changes' safe/deferred subset and reports what it
// skipped — equivalent to ExecuteAccepted with a nil accepted map (no
// blocked change is ever promoted), which is what every caller other than
// goerp#292's accept-triggered resync wants.
func (e *SchemaDiffEngine) Execute(ctx context.Context, sess *SchemaSyncSession, modelDecls []model.ModelDeclaration, changes []schema.Change) ([]schema.Change, error) {
	blocked, _, err := e.ExecuteAccepted(ctx, sess, modelDecls, changes, nil)
	return blocked, err
}

// ExecuteAccepted behaves like Execute, but additionally applies any
// blocked change whose changeHash appears (true) in accepted — goerp#292's
// `POST /admin/schema/accept` writes one system.schema_sync_acceptances
// row per currently-blocked change's hash before triggering a one-time
// resync; this is what that resync calls so exactly the diff(s) an
// operator just authorized apply, and nothing else that's still blocked.
// appliedHashes is every accepted hash that was actually promoted and
// applied this run — the caller (SyncOneAccepted) uses it to mark those
// specific acceptance rows consumed, so a hash can't go on authorizing
// some unrelated future diff that happens to produce the same
// changeHash (see createSchemaSyncAcceptancesTable's own doc comment).
func (e *SchemaDiffEngine) ExecuteAccepted(ctx context.Context, sess *SchemaSyncSession, modelDecls []model.ModelDeclaration, changes []schema.Change, accepted map[string]bool) (blocked []schema.Change, appliedHashes []string, err error) {
	if len(changes) == 0 {
		return nil, nil, nil
	}

	safe, deferred, blockedTC := e.classifyChanges(changes)

	for _, tc := range blockedTC {
		hash := changeHash(tc)
		if accepted[hash] {
			safe = append(safe, tc)
			appliedHashes = append(appliedHashes, hash)
			continue
		}
		blocked = append(blocked, tc.change)
		log.Warn().
			Str("op", fmt.Sprintf("%T", tc.change)).
			Str("tenant", sess.tenantSlug).
			Str("module", sess.moduleName).
			Msg("blocked DDL operation skipped — requires explicit data migration handler")
	}

	if err := e.applyChanges(ctx, sess, modelDecls, safe, deferred, appliedHashes); err != nil {
		return blocked, nil, err
	}

	return blocked, appliedHashes, nil
}

// applyChanges applies safe/deferred, then — in the same dbTx the DDL
// itself commits in — marks every hash in appliedHashes consumed via
// markAcceptancesConsumed below. Doing both in one transaction closes a
// real gap a separate, later call outside this transaction would leave
// open: if that later call ever failed (a transient DB error, the
// process dying between the two calls), the DDL would already be live
// and the acceptance row would
// stay consumed_at = NULL forever — a re-diff afterward never re-proposes
// an already-applied change, so nothing would ever retry marking it, and
// the stale row would stay silently exploitable by some unrelated future
// diff that happens to produce the same changeHash (see
// createSchemaSyncAcceptancesTable's own doc comment in pool.go). Every
// blocked change classify.go ever promotes into safe is a table/column
// alteration, never an index (AddIndex/DropIndex are always already
// "safe", never blocked) — so appliedHashes being non-empty always
// implies the tx/dbTx path below runs, never only the nonTx one.
func (e *SchemaDiffEngine) applyChanges(ctx context.Context, sess *SchemaSyncSession, modelDecls []model.ModelDeclaration, safe, deferred []tableChange, appliedHashes []string) error {
	nonTx, tx := splitNonTransactional(safe)

	for _, tc := range nonTx {
		cmd, err := concurrentIndexDDL(modelDecls, tc.change)
		if err != nil {
			return err
		}
		if err := e.execWithRetry(ctx, sess.conn, cmd); err != nil {
			return fmt.Errorf("DDL failed [%s]: %w", cmd, err)
		}
	}

	for _, tc := range deferred {
		markNotValid(tc.change)
	}
	tx = append(tx, deferred...)

	planChanges := groupForPlanning(tx)
	if len(planChanges) > 0 {
		driver, err := postgres.Open(sess.conn)
		if err != nil {
			return err
		}

		plan, err := driver.PlanChanges(ctx, "goerp_sync", planChanges, func(o *migrate.PlanOptions) {
			o.Mode = migrate.PlanModeInPlace
		})
		if err != nil {
			return err
		}

		dbTx, err := sess.conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		for _, stmt := range plan.Changes {
			if err := e.execWithRetry(ctx, dbTx, stmt.Cmd); err != nil {
				_ = dbTx.Rollback()
				return fmt.Errorf("DDL failed [%s]: %w", stmt.Cmd, err)
			}
		}
		if err := recordPendingValidations(ctx, dbTx, sess.tenantID, sess.tenantSlug, deferred); err != nil {
			_ = dbTx.Rollback()
			return err
		}
		if err := markAcceptancesConsumed(ctx, dbTx, sess.tenantID, sess.moduleName, sess.ModuleVersion(), appliedHashes); err != nil {
			_ = dbTx.Rollback()
			return err
		}
		if err := dbTx.Commit(); err != nil {
			return err
		}
	}

	return nil
}

// markAcceptancesConsumed sets consumed_at on the not-yet-consumed
// system.schema_sync_acceptances row matching each hash under
// moduleVersion, in the same transaction as the DDL that just applied
// them — see applyChanges' own doc comment for why this can't safely be
// a separate, later call. The partial unique index backing this table
// (createSchemaSyncAcceptancesUnconsumedIndex, pool.go) guarantees at
// most one unconsumed row per (tenant, module, version, hash), so this
// UPDATE can only ever affect the one row that actually authorized this
// apply — never an unrelated row from a different version or a
// duplicate insert.
func markAcceptancesConsumed(ctx context.Context, dbTx *sql.Tx, tenantID, moduleName, moduleVersion string, hashes []string) error {
	for _, h := range hashes {
		if _, err := dbTx.ExecContext(ctx, `
			UPDATE system.schema_sync_acceptances
			SET consumed_at = NOW()
			WHERE tenant_id = $1 AND module_name = $2 AND module_version = $3 AND target_hash = $4 AND consumed_at IS NULL
		`, tenantID, moduleName, moduleVersion, h); err != nil {
			return fmt.Errorf("mark schema sync acceptance consumed for hash %s: %w", h, err)
		}
	}
	return nil
}

// markNotValid tags a deferred constraint-add change so PlanChanges emits it
// with a trailing NOT VALID clause instead of applying it fully validated —
// ariga.io/atlas/sql/postgres's migrate.go checks for exactly this clause on
// *schema.AddCheck and *schema.AddForeignKey.
func markNotValid(c schema.Change) {
	switch v := c.(type) {
	case *schema.AddCheck:
		v.Extra = append(v.Extra, &postgres.NotValid{})
	case *schema.AddForeignKey:
		v.Extra = append(v.Extra, &postgres.NotValid{})
	}
}

// deferredConstraintName returns the name of the constraint a deferred
// change creates — schema.AddCheck and schema.AddForeignKey name their
// constraint differently (C.Name vs F.Symbol), so callers that only care
// about "what did we just create as NOT VALID" go through this instead of
// repeating the type switch.
func deferredConstraintName(c schema.Change) (string, error) {
	switch v := c.(type) {
	case *schema.AddCheck:
		return v.C.Name, nil
	case *schema.AddForeignKey:
		return v.F.Symbol, nil
	default:
		return "", fmt.Errorf("deferredConstraintName: unexpected change type %T", c)
	}
}

// recordPendingValidations writes one system.pending_constraint_validations
// row per deferred constraint, in the same transaction as the NOT VALID DDL
// that created it — so "constraint created NOT VALID" and "tracked as
// pending validation" commit or roll back together. ON CONFLICT resets an
// existing row back to pending: a constraint can only reach this path while
// it doesn't yet exist live (Atlas wouldn't re-propose an AddCheck/AddForeignKey
// for a constraint its own inspection already sees), so the only way to see
// a conflicting row here is a previous attempt for the same constraint that
// never got past this same transaction.
func recordPendingValidations(ctx context.Context, dbTx *sql.Tx, tenantID, tenantSlug string, deferred []tableChange) error {
	for _, tc := range deferred {
		name, err := deferredConstraintName(tc.change)
		if err != nil {
			return err
		}
		if _, err := dbTx.ExecContext(ctx, `
			INSERT INTO system.pending_constraint_validations (tenant_id, tenant_slug, table_name, constraint_name, status)
			VALUES ($1, $2, $3, $4, 'pending')
			ON CONFLICT (tenant_id, table_name, constraint_name) DO UPDATE SET
				status = 'pending', error = NULL, validated_at = NULL
		`, tenantID, tenantSlug, tc.table.Name, name); err != nil {
			return fmt.Errorf("record pending validation for %s.%s: %w", tc.table.Name, name, err)
		}
	}
	return nil
}

func splitNonTransactional(changes []tableChange) (nonTx, tx []tableChange) {
	for _, tc := range changes {
		switch tc.change.(type) {
		case *schema.AddIndex, *schema.DropIndex:
			nonTx = append(nonTx, tc)
		default:
			tx = append(tx, tc)
		}
	}
	return
}

func groupForPlanning(changes []tableChange) []schema.Change {
	var out []schema.Change
	byTable := map[*schema.Table][]schema.Change{}
	var order []*schema.Table

	for _, tc := range changes {
		switch tc.change.(type) {
		case *schema.AddTable, *schema.DropTable, *schema.RenameTable:
			out = append(out, tc.change)
		default:
			if tc.table == nil {
				out = append(out, tc.change)
				continue
			}
			if _, seen := byTable[tc.table]; !seen {
				order = append(order, tc.table)
			}
			byTable[tc.table] = append(byTable[tc.table], tc.change)
		}
	}

	for _, t := range order {
		out = append(out, &schema.ModifyTable{T: t, Changes: byTable[t]})
	}
	return out
}

// sqlExecer is satisfied by both *sql.Conn (for non-transactional
// statements) and *sql.Tx (for the batched transaction above) - the same
// retry logic applies to a DDL statement regardless of which one is running
// it.
type sqlExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (e *SchemaDiffEngine) execWithRetry(ctx context.Context, execer sqlExecer, cmd string) error {
	const maxAttempts = 3
	backoff := 200 * time.Millisecond

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		stmtCtx, cancel := context.WithTimeout(ctx, e.statementTimeout())
		_, err := execer.ExecContext(stmtCtx, cmd)
		cancel()
		if err == nil {
			return nil
		}
		if !isRetryableDDLError(err) || attempt == maxAttempts {
			return err
		}
		lastErr = err
		log.Warn().
			Int("attempt", attempt).
			Str("cmd", cmd).
			Err(err).
			Msg("DDL statement failed with a retryable error, retrying")
		time.Sleep(backoff)
		backoff *= 2
	}
	return lastErr
}

func (e *SchemaDiffEngine) statementTimeout() time.Duration {
	if e.cfg == nil || e.cfg.DDLStatementTimeout <= 0 {
		return 30 * time.Second
	}
	return e.cfg.DDLStatementTimeout
}

func isRetryableDDLError(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	switch pgErr.Code {
	case "55P03", "40P01", "40001":
		return true
	default:
		return false
	}
}
