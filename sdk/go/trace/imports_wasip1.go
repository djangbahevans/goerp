//go:build wasip1

package trace

//go:wasmimport host.trace span_start
func hostTraceSpanStart(ptr, size uint32) uint64

//go:wasmimport host.trace span_end
func hostTraceSpanEnd(ptr, size uint32) uint64

//go:wasmimport host.trace span_attr
func hostTraceSpanAttr(ptr, size uint32) uint64
