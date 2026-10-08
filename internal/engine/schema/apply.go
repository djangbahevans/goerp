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

// ExecuteAccepted applies safe/deferred changes plus blocked hashes explicitly accepted by
// the operator. Applied hashes are returned for consumption so consent cannot authorize a
// future change.
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

// applyChanges consumes accepted hashes in the DDL transaction. Atomic consumption
// prevents stale consent from surviving a crash and authorizing a structurally identical
// future change.
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

// Consume matching live acceptance rows in the DDL transaction, pinned to tenant, module,
// version and hash.
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

// Record deferred validation in the NOT VALID DDL transaction so constraint creation and
// its pending work commit or roll back together.
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

func (e *SchemaDiffEngine) execWithRetry(ctx context.Context, execer execQuerier, cmd string) error {
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
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	if !ok {
		return false
	}
	switch pgErr.Code {
	case "55P03", "40P01", "40001":
		return true
	default:
		return false
	}
}
