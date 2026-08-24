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
func (e *SchemaDiffEngine) SyncRLSPolicies(ctx context.Context, sess *SchemaSyncSession, modelDecls []model.ModelDeclaration, policies []manifest.Policy) error {
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

		pgPolicyName := postgresPolicyName(policy.Name)

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
	return nil
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

	return tableNameFor(md), forAll, nil
}

// postgresPolicyName turns a manifest policy name like
// "sales:order:own_only" into the Postgres identifier form
// "sales_order_own_only" used in multitenancy-internals.md §5a's example.
func postgresPolicyName(name string) string {
	return strings.ReplaceAll(name, ":", "_")
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
