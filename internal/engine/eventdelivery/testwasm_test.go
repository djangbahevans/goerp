package eventdelivery

// buildHandleEventConstStatusModule assembles, at the raw WASM binary
// level, a minimal module exporting allocate/deallocate/handle_event,
// where handle_event ignores its input entirely and always returns the
// literal status constant — needed because SubscriberDeliveryWorker/
// SyncDispatcher now pass a full event.Envelope (goerp#129), not the bare
// payload, so a fixture that echoes "request length as status" (the
// style internal/engine/wasm's own handleEventEchoModule uses) can no
// longer hit an exact status of 0/1/2 on demand — the envelope's fixed
// fields make the marshaled length always exceed 2 bytes. Same section
// layout/helpers as internal/engine/wasm's own testwasm_test.go
// (buildHostCallerModule) — duplicated here, not imported, since these
// are unexported test helpers in a different package.
func buildHandleEventConstStatusModule(status int32) []byte {
	const (
		secType   = 1
		secFunc   = 3
		secMemory = 5
		secGlobal = 6
		secExport = 7
		secCode   = 10
		valI32    = 0x7F
	)

	// type0 = (i32)->i32 [allocate], type1 = (i32,i32)->() [deallocate],
	// type2 = (i32,i32)->i32 [handle_event].
	typeSec := concatVec([][]byte{
		{0x60, 0x01, valI32, 0x01, valI32},
		{0x60, 0x02, valI32, valI32, 0x00},
		{0x60, 0x02, valI32, valI32, 0x01, valI32},
	})
	funcSec := prefixCount(3, []byte{0x00, 0x01, 0x02})
	memorySec := []byte{0x01, 0x00, 0x01} // 1 memory, no max, min 1 page

	// Mutable i32 global initialized to 1024 — the same bump-allocator
	// base internal/engine/wasm's fixtures use.
	globalSec := []byte{0x01, valI32, 0x01, 0x41, 0x80, 0x08, 0x0B}

	exportSec := concatVec([][]byte{
		append(encodeName("allocate"), 0x00, 0x00),
		append(encodeName("deallocate"), 0x00, 0x01),
		append(encodeName("handle_event"), 0x00, 0x02),
	})

	allocateBody := []byte{
		0x01, 0x01, valI32, // 1 local declaration group: 1 local of type i32
		0x23, 0x00, // global.get 0
		0x21, 0x01, // local.set 1
		0x20, 0x01, // local.get 1
		0x20, 0x00, // local.get 0
		0x6A,       // i32.add
		0x24, 0x00, // global.set 0
		0x20, 0x01, // local.get 1
		0x0B, // end
	}
	deallocateBody := []byte{0x00, 0x0B} // no locals; end
	// No locals; i32.const <status> (single-byte signed LEB128 for 0-2); end.
	handleEventBody := []byte{0x00, 0x41, byte(status), 0x0B}

	codeSec := concatVec([][]byte{
		prefixLen(allocateBody),
		prefixLen(deallocateBody),
		prefixLen(handleEventBody),
	})

	var m []byte
	m = append(m, 0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00) // magic + version
	m = append(m, section(secType, typeSec)...)
	m = append(m, section(secFunc, funcSec)...)
	m = append(m, section(secMemory, memorySec)...)
	m = append(m, section(secGlobal, globalSec)...)
	m = append(m, section(secExport, exportSec)...)
	m = append(m, section(secCode, codeSec)...)
	return m
}

func section(id byte, content []byte) []byte {
	out := []byte{id}
	out = append(out, uLEB128(uint32(len(content)))...)
	return append(out, content...)
}

func prefixCount(count int, body []byte) []byte {
	out := uLEB128(uint32(count))
	return append(out, body...)
}

func prefixLen(body []byte) []byte {
	out := uLEB128(uint32(len(body)))
	return append(out, body...)
}

func concatVec(items [][]byte) []byte {
	out := uLEB128(uint32(len(items)))
	for _, item := range items {
		out = append(out, item...)
	}
	return out
}

func encodeName(name string) []byte {
	return append(uLEB128(uint32(len(name))), []byte(name)...)
}

// uLEB128 encodes v as an unsigned LEB128 varint — the integer encoding
// every WASM binary section-length/count/index field uses.
func uLEB128(v uint32) []byte {
	var out []byte
	for {
		b := byte(v & 0x7F)
		v >>= 7
		if v != 0 {
			b |= 0x80
		}
		out = append(out, b)
		if v == 0 {
			return out
		}
	}
}
