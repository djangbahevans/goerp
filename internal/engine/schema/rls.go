package schema

import (
	"context"
	"fmt"
	"strings"

	"github.com/djangbahevans/goerp/internal/engine/db"
	"github.com/djangbahevans/goerp/internal/engine/domain"
	"github.com/djangbahevans/goerp/internal/engine/manifest"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// SyncRLSPolicies compiles each of the module's ABAC policies (manifest
// `policies[]`, manifest-spec.md §8) to a Postgres Row-Level Security
// policy and installs it, per multitenancy-internals.md §5a. It runs after
// DDL apply (Execute) in the same schema-sync sequence, over the same
// connection/session, so newly-created tables already exist by the time a
// policy attaches to them.
//
// applies_to's permission-name validity was already checked at manifest
// load time (validatePolicies, internal/engine/manifest); this step only
// needs to resolve which table each policy's applies_to targets.
// Called with policies == nil for a module uninstall — reconciliation
// below then has nothing desired and drops every policy this module ever
// owned, exactly as it would for any other policy that dropped out of the
// manifest. modelDecls must still reflect the module's owned tables in
// that case (its last-loaded ModelDecls, read before the registry drops
// the module) for reconciliation to know which tables to check.
func (e *SchemaDiffEngine) SyncRLSPolicies(ctx context.Context, sess *SchemaSyncSession, modelDecls []model.ModelDeclaration, policies []manifest.Policy) error {
	desired := make(map[string]string, len(policies)) // pg policy name -> table

	for _, policy := range policies {
		table, forAll, err := resolvePolicyTarget(policy, modelDecls)
		if err != nil {
			return fmt.Errorf("policy %q: %w", policy.Name, err)
		}

		expr, err := domain.Parse(policy.Condition)
		if err != nil {
			return fmt.Errorf("policy %q: condition failed to parse: %w", policy.Name, err)
		}
		compiled, err := domain.CompileToRLS(expr)
		if err != nil {
			return fmt.Errorf("policy %q: %w", policy.Name, err)
		}

		// The Postgres policy is named after the manifest policy's own
		// `name` verbatim, colons included — quoteIdent's quoting already
		// handles arbitrary identifier text, and keeping the `:` intact
		// is what lets reconcileRLSPolicies recover the module segment
		// unambiguously below.
		pgPolicyName := policy.Name
		desired[pgPolicyName] = table

		enableRLS := fmt.Sprintf("ALTER TABLE %s ENABLE ROW LEVEL SECURITY", quoteIdent(table))
		if err := e.execWithRetry(ctx, sess.conn, enableRLS); err != nil {
			return fmt.Errorf("policy %q: enable RLS on %s: %w", policy.Name, table, err)
		}

		if err := e.dropAndCreatePolicy(ctx, sess, pgPolicyName, table, compiled, forAll); err != nil {
			return fmt.Errorf("policy %q: %w", policy.Name, err)
		}
	}

	if err := e.syncShareWidening(ctx, sess, modelDecls, desired); err != nil {
		return err
	}

	return e.reconcileRLSPolicies(ctx, sess, modelDecls, desired)
}

// syncShareWidening installs a separate permissive RLS policy per
// .Shareable() model/SharePermission (multitenancy-internals.md §5a) —
// Postgres already ORs multiple permissive policies on the same
// table/command, so this has the same effect as appending an
// `OR EXISTS(...)` to each ABAC policy above, without editing their
// compiled expressions. Skips a table with no ABAC policy in desired
// (go-sdk-reference.md §22: no restrictive base policy, so widening
// would be redundant).
func (e *SchemaDiffEngine) syncShareWidening(ctx context.Context, sess *SchemaSyncSession, modelDecls []model.ModelDeclaration, desired map[string]string) error {
	tableEnsured := false

	for _, md := range modelDecls {
		if !md.Shareable || len(md.SharePerms) == 0 {
			continue
		}
		table := TableNameFor(md)
		if !tableHasDesiredPolicy(desired, table) {
			continue
		}
		pkCol, pkKind, err := primaryKeyColumnName(md)
		if err != nil {
			return err
		}
		if pkKind != model.KindUUID {
			// record_shares.record_id is UUID — a non-UUID PK would
			// otherwise surface as an opaque Postgres "operator does not
			// exist" error the first time this policy is evaluated.
			return fmt.Errorf("model %s: .Shareable() requires a UUID primary key", md.Name)
		}
		qualifiedName := sess.moduleName + "." + md.Name

		if !tableEnsured {
			// record_shares is normally created by tenant provisioning's
			// CreateEngineTables activity, once, at tenant-creation time —
			// never revisited by this package's own per-module sync. A
			// tenant whose schema predates this feature would otherwise
			// hard-fail every module's sync the moment any .Shareable()
			// model tries to install a policy referencing a table that
			// doesn't exist yet. IF NOT EXISTS makes this a no-op on
			// every other sync once the table is there.
			if err := e.ensureRecordSharesTable(ctx, sess); err != nil {
				return fmt.Errorf("ensure record_shares: %w", err)
			}
			tableEnsured = true
		}

		for _, perm := range md.SharePerms {
			var suffix string
			var forAll bool
			switch perm {
			case model.ReadShare:
				suffix, forAll = "__share_read", false
			case model.WriteShare:
				suffix, forAll = "__share_write", true
			default:
				return fmt.Errorf("model %s: unrecognized SharePermission %q", md.Name, perm)
			}

			// ":"-prefixed with sess.moduleName, matching the ABAC
			// policies above — reconcileRLSPolicies's ownership match
			// depends on that first segment.
			pgPolicyName := sess.moduleName + ":" + md.Name + ":" + suffix
			// record_shares has its own "id" column, so the PK reference
			// must be table-qualified or it resolves to record_shares.id
			// inside this correlated subquery instead of the outer row.
			// NULLIF(...,'') guards against a session (e.g. a workflow
			// activity dispatched with no live user, modCtx.UserID == "")
			// where app.current_user_id is set to an empty string rather
			// than left unset — a bare ''::uuid cast errors outright,
			// unlike every other table's read/write, which never
			// evaluates this cast unless its own ABAC condition happens
			// to reference current_user.id.
			condition := fmt.Sprintf(
				`EXISTS (SELECT 1 FROM record_shares WHERE model = '%s' AND record_id = %s.%s AND shared_with_user_id = NULLIF(current_setting('app.current_user_id', true), '')::uuid AND permission = '%s' AND (expires_at IS NULL OR expires_at > NOW()))`,
				domain.EscapeSQLString(qualifiedName), quoteIdent(table), quoteIdent(pkCol), domain.EscapeSQLString(string(perm)),
			)

			desired[pgPolicyName] = table

			if err := e.dropAndCreatePolicy(ctx, sess, pgPolicyName, table, condition, forAll); err != nil {
				return fmt.Errorf("share policy %q: %w", pgPolicyName, err)
			}
		}
	}
	return nil
}

// ensureRecordSharesTable creates record_shares in the syncing session's
// tenant schema if it doesn't already exist yet — mirrors
// internal/engine/recordshares.Store.Bootstrap's own DDL, duplicated
// rather than called directly since that Store takes a *sql.DB (to open
// its own advisory-locked transaction) while this runs inside the
// already-open *sql.Conn a schema-sync session shares across every
// statement it issues. Takes the identical advisory lock key Bootstrap
// itself takes (via pg_advisory_xact_lock instead of pool.BeginTx, since
// sess.conn is a single already-checked-out connection, not a *sql.DB) —
// two syncs for different modules against the same tenant can run
// concurrently (BeginSync's own lock is scoped to (tenant, module), not
// tenant alone), and this is the exact concurrent-CREATE-TABLE race
// goerp#171 already fixed once for Bootstrap; reusing its lock key
// serializes against both Bootstrap and every other sync's own call here.
func (e *SchemaDiffEngine) ensureRecordSharesTable(ctx context.Context, sess *SchemaSyncSession) error {
	tx, err := sess.conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	lockKey := db.AdvisoryLockKey("recordshares.Bootstrap:" + sess.tenantSlug)
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", lockKey); err != nil {
		return fmt.Errorf("acquire advisory lock: %w", err)
	}

	const createTable = `
		CREATE TABLE IF NOT EXISTS record_shares (
		    id                   UUID PRIMARY KEY DEFAULT uuidv7(),
		    model                TEXT NOT NULL,
		    record_id            UUID NOT NULL,
		    shared_with_user_id  UUID NOT NULL,
		    permission           TEXT NOT NULL CHECK (permission IN ('read', 'write')),
		    shared_by            UUID NOT NULL,
		    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		    expires_at           TIMESTAMPTZ
		)
	`
	if _, err := tx.ExecContext(ctx, createTable); err != nil {
		return fmt.Errorf("create table: %w", err)
	}

	const createIndex = `
		CREATE INDEX IF NOT EXISTS idx_record_shares_lookup
		    ON record_shares(model, record_id, shared_with_user_id)
	`
	if _, err := tx.ExecContext(ctx, createIndex); err != nil {
		return fmt.Errorf("create lookup index: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// dropAndCreatePolicy replaces table's pgPolicyName policy — DROP POLICY
// IF EXISTS, then CREATE POLICY — shared by the ABAC loop above and
// syncShareWidening.
func (e *SchemaDiffEngine) dropAndCreatePolicy(ctx context.Context, sess *SchemaSyncSession, pgPolicyName, table, condition string, forAll bool) error {
	dropStmt := fmt.Sprintf("DROP POLICY IF EXISTS %s ON %s", quoteIdent(pgPolicyName), quoteIdent(table))
	if err := e.execWithRetry(ctx, sess.conn, dropStmt); err != nil {
		return fmt.Errorf("drop existing policy: %w", err)
	}
	createStmt := buildCreatePolicyStmt(pgPolicyName, table, condition, forAll)
	if err := e.execWithRetry(ctx, sess.conn, createStmt); err != nil {
		return fmt.Errorf("create policy: %w", err)
	}
	return nil
}

// tableHasDesiredPolicy reports whether desired (this module's own ABAC
// policies) targets table.
func tableHasDesiredPolicy(desired map[string]string, table string) bool {
	for _, t := range desired {
		if t == table {
			return true
		}
	}
	return false
}

// primaryKeyColumnName returns md's single IsPrimaryKey-flagged field's
// name and kind — record_shares.record_id is one UUID column, so a
// composite key (more than one IsPrimaryKey field, which atlas.go's own
// toAtlasTable otherwise supports) is rejected rather than silently
// keying widening off just the first one found.
func primaryKeyColumnName(md model.ModelDeclaration) (name string, kind model.FieldKind, err error) {
	found := false
	for _, f := range md.Fields {
		if !f.Def.IsPrimaryKey {
			continue
		}
		if found {
			return "", 0, fmt.Errorf("model %s: .Shareable() does not support a composite primary key", md.Name)
		}
		name, kind, found = f.Name, f.Def.Kind, true
	}
	if !found {
		return "", 0, fmt.Errorf("model %s: .Shareable() requires a declared primary key field", md.Name)
	}
	return name, kind, nil
}

// reconcileRLSPolicies walks the live pg_policies catalog for every table
// the module owns and drops whichever of its own policies aren't in
// desired (built by the loop above from the module's current manifest) —
// a policy renamed or deleted from the manifest, or, when desired is
// empty, every policy the module ever owned. A table left with none after
// dropping gets RLS disabled too, so it doesn't fail closed against a
// policy that no longer exists.
//
// A table's live policies can include a different module's policy too (a
// field_extension-style shared table, multitenancy-internals.md §5a).
// Ownership is an exact match on a live name's first `:`-delimited
// segment against this module's own name — manifest-spec.md §8's `name`
// format always starts with the declaring module, and §2's module-name
// regex excludes `:` from every module name, so the split is lossless,
// unlike a prefix match on an underscore-joined name (which
// "connector_paystack" and "connector_paystack_v2" could both satisfy).
// RLS stays enabled if any live policy remains regardless of ownership,
// so a foreign policy surviving this pass keeps the table's RLS on.
func (e *SchemaDiffEngine) reconcileRLSPolicies(ctx context.Context, sess *SchemaSyncSession, modelDecls []model.ModelDeclaration, desired map[string]string) error {
	schemaName := "tenant_" + sess.tenantSlug
	tables := dedupedOwnedTables(modelDecls)

	liveByTable, err := listRLSPoliciesByTable(ctx, sess.conn, schemaName, tables)
	if err != nil {
		return fmt.Errorf("list RLS policies: %w", err)
	}

	for _, table := range tables {
		liveNames := liveByTable[table]

		anyRemain := false
		for _, name := range liveNames {
			owningModule, _, ok := strings.Cut(name, ":")
			if !ok || owningModule != sess.moduleName {
				anyRemain = true
				continue
			}
			// desired maps a policy name to the one table it's
			// currently declared against — a name can appear here for a
			// *different* table (its applies_to moved to another
			// resource across a manifest edit; validatePolicies never
			// requires a policy's name and applies_to to reference the
			// same resource), so only that exact (name, table) pairing
			// counts as still desired. Anything else is this module's
			// own stale leftover on this table.
			if desiredTable, stillDesired := desired[name]; stillDesired && desiredTable == table {
				anyRemain = true
				continue
			}
			dropStmt := fmt.Sprintf("DROP POLICY IF EXISTS %s ON %s", quoteIdent(name), quoteIdent(table))
			if err := e.execWithRetry(ctx, sess.conn, dropStmt); err != nil {
				return fmt.Errorf("drop policy %q on %s: %w", name, table, err)
			}
		}

		// Nothing to disable when the table never had a live policy to
		// begin with, or when something (this module's own or a
		// foreign one) is still standing after the loop above.
		if len(liveNames) == 0 || anyRemain {
			continue
		}
		disableRLS := fmt.Sprintf("ALTER TABLE %s DISABLE ROW LEVEL SECURITY", quoteIdent(table))
		if err := e.execWithRetry(ctx, sess.conn, disableRLS); err != nil {
			return fmt.Errorf("disable RLS on %s: %w", table, err)
		}
	}

	return nil
}

// listRLSPolicyNames returns every policy name Postgres has installed on
// table, regardless of which module created it — reconcileRLSPolicies is
// what narrows this down to the names it can attribute to the syncing
// module before treating any of them as a drop candidate.
func listRLSPolicyNames(ctx context.Context, execer execQuerier, schemaName, table string) ([]string, error) {
	rows, err := execer.QueryContext(ctx,
		"SELECT policyname FROM pg_policies WHERE schemaname = $1 AND tablename = $2",
		schemaName, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// listRLSPoliciesByTable is listRLSPolicyNames batched across every table
// in one round-trip (internal/engine/eventdelivery/replay.go's own
// `= ANY($1)` precedent) instead of one query per table, grouped by
// tablename for reconcileRLSPolicies's per-table walk.
func listRLSPoliciesByTable(ctx context.Context, execer execQuerier, schemaName string, tables []string) (map[string][]string, error) {
	rows, err := execer.QueryContext(ctx,
		"SELECT tablename, policyname FROM pg_policies WHERE schemaname = $1 AND tablename = ANY($2)",
		schemaName, pqStringArray(tables))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byTable := make(map[string][]string, len(tables))
	for rows.Next() {
		var table, name string
		if err := rows.Scan(&table, &name); err != nil {
			return nil, err
		}
		byTable[table] = append(byTable[table], name)
	}
	return byTable, rows.Err()
}

// pqStringArray formats a Go string slice as a Postgres text[] literal for
// an `= ANY($1)` match — database/sql has no native []string binding, and
// this package has no pq/pgtype array-encoding wired into its plain
// *sql.Conn/*sql.DB use (same rationale as eventdelivery's own copy of
// this helper). Table names come from TableNameFor (snake_case(Name) or
// an explicit Table override), not free-form user input, but escaping
// costs nothing.
func pqStringArray(vals []string) string {
	quoted := make([]string, len(vals))
	for i, v := range vals {
		escaped := strings.ReplaceAll(v, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `"`, `\"`)
		quoted[i] = `"` + escaped + `"`
	}
	return "{" + strings.Join(quoted, ",") + "}"
}

// resolvePolicyTarget resolves a policy's applies_to
// (`{module}:{resource}:{action}`, manifest-spec.md §8) to its target
// table and whether it governs writes (`FOR ALL`) or only reads
// (`FOR SELECT`). The resource segment is matched against a declared
// model's bare Name or its module-qualified `{module}.{resource}` form,
// since neither the manifest nor the model registry link a permission to
// a model directly (goerp#71's own scope note).
func resolvePolicyTarget(policy manifest.Policy, modelDecls []model.ModelDeclaration) (table string, forAll bool, err error) {
	parts := strings.Split(policy.AppliesTo, ":")
	if len(parts) != 3 {
		return "", false, fmt.Errorf("applies_to %q must be in {module}:{resource}:{action} form", policy.AppliesTo)
	}
	module, resource, action := parts[0], parts[1], parts[2]
	qualified := module + "." + resource

	var md model.ModelDeclaration
	found := false
	for _, d := range modelDecls {
		if d.Name == resource || d.Name == qualified {
			md = d
			found = true
			break
		}
	}
	if !found {
		return "", false, fmt.Errorf("applies_to %q: no declared model matches resource %q", policy.AppliesTo, resource)
	}

	switch action {
	case "read":
		forAll = false
	case "write", "delete":
		forAll = true
	default:
		return "", false, fmt.Errorf("applies_to %q: unrecognized action %q (expected read/write/delete)", policy.AppliesTo, action)
	}

	return TableNameFor(md), forAll, nil
}

func buildCreatePolicyStmt(pgPolicyName, table, compiledExpr string, forAll bool) string {
	command := "SELECT"
	if forAll {
		command = "ALL"
	}
	stmt := fmt.Sprintf("CREATE POLICY %s ON %s FOR %s USING (%s)",
		quoteIdent(pgPolicyName), quoteIdent(table), command, compiledExpr)
	if forAll {
		stmt += fmt.Sprintf(" WITH CHECK (%s)", compiledExpr)
	}
	return stmt
}
