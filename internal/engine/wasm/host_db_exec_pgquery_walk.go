package wasm

import "google.golang.org/protobuf/reflect/protoreflect"

// walkPGQueryTree stops descending when visit returns false, allowing callers to exclude
// subqueries or other scope boundaries.
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
			for i := range list.Len() {
				walkPGQueryTree(list.Get(i).Message(), visit)
			}
			return true
		}
		walkPGQueryTree(v.Message(), visit)
		return true
	})
}
