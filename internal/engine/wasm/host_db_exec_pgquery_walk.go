package wasm

import "google.golang.org/protobuf/reflect/protoreflect"

// walkPGQueryTree recursively visits every message-kind field reachable
// from m (in whatever order protoreflect.Message.Range yields them),
// calling visit at each node, m itself first. visit returning false
// tells walkPGQueryTree not to descend into that node's own children —
// how a caller stops at a scope boundary its search shouldn't cross (a
// subquery, a disjunctive OR branch, a node with nothing useful
// underneath) without writing its own separate recursion. Shared by
// host_db_exec_audit.go's renumberParams and host_db_exec_etag.go's
// whereClauseHasEtagCheck, the two pg_query AST walks this package
// needs.
func walkPGQueryTree(m protoreflect.Message, visit func(protoreflect.Message) bool) {
	if !m.IsValid() || !visit(m) {
		return
	}
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		if fd.Kind() != protoreflect.MessageKind {
			return true
		}
		if fd.IsList() {
			list := v.List()
			for i := 0; i < list.Len(); i++ {
				walkPGQueryTree(list.Get(i).Message(), visit)
			}
			return true
		}
		walkPGQueryTree(v.Message(), visit)
		return true
	})
}
