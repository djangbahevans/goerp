//go:build !wasip1

package ui

func hostUIPush(ptr, size uint32) uint64 {
	panic("sdk/go/ui: host.ui.push is only available in a wasip1 build")
}
