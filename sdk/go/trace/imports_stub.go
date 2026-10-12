//go:build !wasip1

package trace

func hostTraceSpanStart(ptr, size uint32) uint64 {
	panic("sdk/go/trace: host.trace.span_start is only available in a wasip1 build")
}

func hostTraceSpanEnd(ptr, size uint32) uint64 {
	panic("sdk/go/trace: host.trace.span_end is only available in a wasip1 build")
}

func hostTraceSpanAttr(ptr, size uint32) uint64 {
	panic("sdk/go/trace: host.trace.span_attr is only available in a wasip1 build")
}
