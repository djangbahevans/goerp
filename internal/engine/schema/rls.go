package schema

import (
	"context"
	"fmt"
	"strings"

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

		dropStmt := fmt.Sprintf("DROP POLICY IF EXISTS %s ON %s", quoteIdent(pgPolicyName), quoteIdent(table))
		if err := e.execWithRetry(ctx, sess.conn, dropStmt); err != nil {
			return fmt.Errorf("policy %q: drop existing policy: %w", policy.Name, err)
		}

		createStmt := buildCreatePolicyStmt(pgPolicyName, table, compiled, forAll)
		if err := e.execWithRetry(ctx, sess.conn, createStmt); err != nil {
			return fmt.Errorf("policy %q: create policy: %w", policy.Name, err)
		}
	}

	return e.reconcileRLSPolicies(ctx, sess, modelDecls, desired)
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
