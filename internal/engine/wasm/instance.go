package wasm

import (
	"sync/atomic"

	"github.com/tetratelabs/wazero/api"
)

type ModuleInstance struct {
	module        api.Module
	memory        api.Memory
	allocate      api.Function
	deallocate    api.Function
	handleRequest api.Function
	handleEvent   api.Function
	handleJob     api.Function
	inUse         atomic.Bool
}
