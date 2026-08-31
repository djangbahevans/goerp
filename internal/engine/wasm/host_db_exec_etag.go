package wasm

import (
	pg_query "github.com/pganalyze/pg_query_go/v6"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/djangbahevans/goerp/sdk/go/model"
)

// This file is the etag-enforcement mechanism host-abi-reference.md §5
// "Etag enforcement" describes for host.db.exec (goerp#460, not yet
// built — see host_db_exec_audit.go's own doc comment for why). Scope
// note: the doc's "if absent, the engine appends [an etag clause]
// automatically using the etag value from the corresponding SELECT (if
// available in the request context)" describes a per-request
// etag-observation cache with no defined mechanics anywhere in the docs
// — go-sdk-reference.md's db.UpdateByID takes no etag argument at all,
// so whatever tracks "the request context" would have to live
// engine-side, and nothing specifies its keying or lifetime. That part
// is deliberately out of scope here. What's built is the fully-specified
// core: whether an UPDATE's own WHERE clause already checks etag, and,
// if so, translating a zero-rows-affected write into db.etag_mismatch
// rather than a bare empty result. A module that writes no etag clause
// of its own gets no enforcement on that statement, the same as
// opts.skip_etag — "recommended for all mutable records" is guidance,
// not a hard gate this mechanism enforces on the module's behalf.

// resolveEtagTable resolves table (a bare name from raw SQL) against
// modCtx's declared models. hasEtag is false if no declared model owns
// table, or that model has no etag column — either way, host.db.exec
// has nothing to enforce for this UPDATE.
func resolveEtagTable(modCtx *ModuleContext, table string) (md model.ModelDeclaration, hasEtag bool) {
	for _, decl := range modCtx.ModelDecls() {
		if tableNameForORM(decl) == table {
			return decl, hasField(decl, "etag")
		}
	}
	return model.ModelDeclaration{}, false
}

// whereClauseHasEtagCheck reports whether whereClause, the WHERE clause
// of an UPDATE against relation, contains an equality comparison against
// relation's own etag column — bare ("etag") or qualified by relation's
// own name or alias ("t.etag") — reachable through nothing but AND from
// the top. Walked via walkPGQueryTree (host_db_exec_pgquery_walk.go).
// Three things intentionally stop the walk from crediting a nested
// comparison as this statement's own protection:
//   - A SubLink's own Subselect: "WHERE id IN (SELECT ... FROM other
//     WHERE etag = $n)" checks a different relation's etag entirely.
//   - A BoolExpr that isn't AND (OR, NOT): "WHERE id = $1 OR etag = $2"
//     can update a row whose etag was never checked, since the OR's
//     other branch alone can satisfy the condition — only a conjunct
//     every matched row must satisfy is a real check.
//   - A ColumnRef qualified to a different table/alias than relation's
//     own: "UPDATE widget ... FROM other WHERE other.etag = $n" checks
//     other's etag, not widget's — see columnRefNamesEtag.
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

// columnRefNamesEtag reports whether node is a ColumnRef naming an
// "etag" column that belongs to the UPDATE's own target relation.
// Unqualified ("etag") is assumed to be the target's own column: if the
// statement also references a different table's etag column via a FROM
// clause, an unqualified reference would only be valid SQL at all
// (Postgres rejects an ambiguous unqualified reference outright) when
// exactly one of the tables in scope actually has that column — and
// resolveEtagTable's own caller (host.db.exec, once built) only reaches
// this check when the target table has one. A qualified reference
// ("other.etag") only counts when the qualifier matches targetNames —
// see targetRelationNames and whereClauseHasEtagCheck's own doc comment
// for why a differently-qualified one doesn't.
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
