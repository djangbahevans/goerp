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

// whereClauseHasEtagCheck reports whether whereClause contains an
// equality comparison against an "etag" column — bare or table-qualified
// (e.g. "t.etag") — anywhere in its own top-level boolean expression
// tree, regardless of AND/OR nesting. Walked via protobuf reflection
// rather than a type switch over every SQL expression node a WHERE
// clause can contain, the same approach renumberParams
// (host_db_exec_audit.go) uses for its own tree walk. Does not recurse
// into a SubLink's own Subselect: an "etag = $n" inside a subquery (e.g.
// WHERE id IN (SELECT ... FROM other_table WHERE etag = $n)) scopes a
// different relation entirely, not the row this UPDATE targets, so
// treating it as this statement's own etag check would be a false
// positive.
func whereClauseHasEtagCheck(whereClause *pg_query.Node) bool {
	if whereClause == nil {
		return false
	}

	found := false
	var walk func(m protoreflect.Message)
	walk = func(m protoreflect.Message) {
		if !m.IsValid() || found {
			return
		}
		if expr, ok := m.Interface().(*pg_query.A_Expr); ok {
			if expr.Kind == pg_query.A_Expr_Kind_AEXPR_OP && aExprNameIsEquality(expr.Name) &&
				(columnRefNamesEtag(expr.Lexpr) || columnRefNamesEtag(expr.Rexpr)) {
				found = true
			}
			return
		}
		if _, ok := m.Interface().(*pg_query.SubLink); ok {
			return
		}
		m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
			if fd.Kind() != protoreflect.MessageKind {
				return true
			}
			if fd.IsList() {
				list := v.List()
				for i := 0; i < list.Len(); i++ {
					walk(list.Get(i).Message())
				}
				return true
			}
			walk(v.Message())
			return true
		})
	}
	walk(whereClause.ProtoReflect())
	return found
}

func aExprNameIsEquality(name []*pg_query.Node) bool {
	for _, n := range name {
		if s, ok := n.GetNode().(*pg_query.Node_String_); ok && s.String_.GetSval() == "=" {
			return true
		}
	}
	return false
}

func columnRefNamesEtag(node *pg_query.Node) bool {
	if node == nil {
		return false
	}
	cr, ok := node.GetNode().(*pg_query.Node_ColumnRef)
	if !ok || len(cr.ColumnRef.GetFields()) == 0 {
		return false
	}
	fields := cr.ColumnRef.GetFields()
	last, ok := fields[len(fields)-1].GetNode().(*pg_query.Node_String_)
	return ok && last.String_.GetSval() == "etag"
}

// isEtagMismatch reports whether a zero-rows-affected UPDATE should be
// reported as db.etag_mismatch rather than a bare empty result: the
// statement's own WHERE clause already checked etag, so zero rows means
// the check failed, not merely that the target doesn't exist.
func isEtagMismatch(whereClauseHasEtag bool, rowsAffected int64) bool {
	return whereClauseHasEtag && rowsAffected == 0
}
