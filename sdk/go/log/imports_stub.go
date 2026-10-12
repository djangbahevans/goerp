//go:build !wasip1

package log

func hostLog(ptr, size uint32) uint64 {
	panic("sdk/go/log: host.log is only available in a wasip1 build")
}
