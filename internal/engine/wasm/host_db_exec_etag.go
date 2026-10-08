package wasm

import (
	pg_query "github.com/pganalyze/pg_query_go/v6"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/djangbahevans/goerp/internal/engine/modeltable"
	"github.com/djangbahevans/goerp/sdk/go/model"
)

// Raw UPDATE etag enforcement applies only when the statement supplies an etag predicate;
// a zero-row result becomes db.etag_mismatch. The host does not infer etags from earlier
// reads.

// resolveEtagTable resolves table (a bare name from raw SQL) against
// modCtx's declared models. hasEtag is false if no declared model owns
// table, or that model has no etag column — either way, host.db.exec
// has nothing to enforce for this UPDATE.
func resolveEtagTable(modCtx *ModuleContext, table string) (md model.ModelDeclaration, hasEtag bool) {
	for _, decl := range modCtx.ModelDecls() {
		if modeltable.Name(decl) == table {
			return decl, hasField(decl, "etag")
		}
	}
	return model.ModelDeclaration{}, false
}

// Only an etag equality reachable through AND protects every updated row. Subqueries,
// OR/NOT branches, and references to another relation cannot provide that guarantee.
func whereClauseHasEtagCheck(whereClause *pg_query.Node, relation *pg_query.RangeVar) bool {
	if whereClause == nil {
		return false
	}
	targetNames := targetRelationNames(relation)

	found := false
	walkPGQueryTree(whereClause.ProtoReflect(), func(m protoreflect.Message) bool {
		if found {
			return false
		}
		if expr, ok := m.Interface().(*pg_query.A_Expr); ok {
			if expr.Kind == pg_query.A_Expr_Kind_AEXPR_OP && aExprNameIsEquality(expr.Name) &&
				(columnRefNamesEtag(expr.Lexpr, targetNames) || columnRefNamesEtag(expr.Rexpr, targetNames)) {
				found = true
			}
			return false // an A_Expr has no useful children for this search
		}
		if _, ok := m.Interface().(*pg_query.SubLink); ok {
			return false
		}
		if be, ok := m.Interface().(*pg_query.BoolExpr); ok && be.Boolop != pg_query.BoolExprType_AND_EXPR {
			return false
		}
		return true
	})
	return found
}

// targetRelationNames returns the names an "etag" ColumnRef's qualifier
// must match to count as relation's own column: relation's real name,
// plus its alias if the UPDATE gave it one (e.g. "UPDATE widget AS w
// ... WHERE w.etag = $1").
func targetRelationNames(relation *pg_query.RangeVar) map[string]bool {
	names := map[string]bool{}
	if relation == nil {
		return names
	}
	if name := relation.GetRelname(); name != "" {
		names[name] = true
	}
	if alias := relation.GetAlias(); alias != nil && alias.GetAliasname() != "" {
		names[alias.GetAliasname()] = true
	}
	return names
}

func aExprNameIsEquality(name []*pg_query.Node) bool {
	for _, n := range name {
		if s, ok := n.GetNode().(*pg_query.Node_String_); ok && s.String_.GetSval() == "=" {
			return true
		}
	}
	return false
}

// Only the target relation's etag qualifies as a precondition. An unqualified etag is
// valid only when unambiguous; other tables' qualified etags do not count.
func columnRefNamesEtag(node *pg_query.Node, targetNames map[string]bool) bool {
	if node == nil {
		return false
	}
	cr, ok := node.GetNode().(*pg_query.Node_ColumnRef)
	if !ok {
		return false
	}
	fields := cr.ColumnRef.GetFields()
	if len(fields) == 0 {
		return false
	}
	last, ok := fields[len(fields)-1].GetNode().(*pg_query.Node_String_)
	if !ok || last.String_.GetSval() != "etag" {
		return false
	}
	if len(fields) == 1 {
		return true
	}
	qualifier, ok := fields[len(fields)-2].GetNode().(*pg_query.Node_String_)
	return ok && targetNames[qualifier.String_.GetSval()]
}

// isEtagMismatch reports whether a zero-rows-affected UPDATE should be
// reported as db.etag_mismatch rather than a bare empty result: the
// statement's own WHERE clause already checked etag, so zero rows means
// the check failed, not merely that the target doesn't exist.
func isEtagMismatch(whereClauseHasEtag bool, rowsAffected int64) bool {
	return whereClauseHasEtag && rowsAffected == 0
}
