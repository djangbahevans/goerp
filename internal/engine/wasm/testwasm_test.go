package wasm

// buildHostCallerModule assembles, at the raw WASM binary level, a minimal
// module that imports each of funcNames from namespace (all with the
// signature every host function uses, (i32,i32)->i64) and re-exports each
// as "call_"+name, forwarding (ptr, len) straight through — plus a bump-
// allocator "allocate"/no-op "deallocate" pair, the same convention
// invoke_test.go's boundaryTestModule and host_db_test.go's
// hostDBCallerModule hand-assembled individually. This is the generalized,
// programmatic version of that same fixed shape, so a new host.* namespace
// test doesn't need its own hand-toggled hex blob — it only needs to name
// its functions.
func buildHostCallerModule(namespace string, funcNames []string) []byte {
	const (
		secType    = 1
		secImport  = 2
		secFunc    = 3
		secMemory  = 5
		secGlobal  = 6
		secExport  = 7
		secCode    = 10
		valI32     = 0x7F
		valI64     = 0x7E
		externFunc = 0x00
	)

	// Type section: type0 = (i32)->i32 [allocate], type1 = (i32,i32)->()
	// [deallocate], type2 = (i32,i32)->i64 [every host function and its
	// forwarding wrapper].
	typeSec := concatVec([][]byte{
		{0x60, 0x01, valI32, 0x01, valI32},
		{0x60, 0x02, valI32, valI32, 0x00},
		{0x60, 0x02, valI32, valI32, 0x01, valI64},
	})

	// Import section: one func import per funcNames entry, all type2.
	var importItems [][]byte
	for _, name := range funcNames {
		item := append([]byte{}, encodeVecBytes(uLEB128(uint32(len(namespace))), []byte(namespace))...)
		item = append(item, encodeName(name)...)
		item = append(item, externFunc, 0x02)
		importItems = append(importItems, item)
	}
	importSec := concatVec(importItems)

	// Function section: allocate(type0), deallocate(type1), then one
	// call_<name> (type2) per funcNames entry.
	funcSecBody := append([]byte{0x00, 0x01}, bytesRepeat(0x02, len(funcNames))...)
	funcSec := prefixCount(len(funcNames)+2, funcSecBody)

	memorySec := []byte{0x01, 0x00, 0x01} // 1 memory, no max, min 1 page

	// Global: one mutable i32 initialized to 1024 (bump-allocator base),
	// matching hostDBCallerModule's own convention.
	globalSec := []byte{0x01, valI32, 0x01, 0x41, 0x80, 0x08, 0x0B}

	numImports := len(funcNames)
	allocateIdx := uint32(numImports)
	deallocateIdx := uint32(numImports + 1)

	// Export section: allocate, deallocate, then call_<name> per function,
	// pointing at local function indices numImports+2+i (imports occupy
	// indices 0..numImports-1).
	exportItems := [][]byte{
		append(encodeName("allocate"), 0x00, byte(allocateIdx)),
		append(encodeName("deallocate"), 0x00, byte(deallocateIdx)),
	}
	for i, name := range funcNames {
		exportItems = append(exportItems, append(encodeName("call_"+name), 0x00, byte(numImports+2+i)))
	}
	exportSec := concatVec(exportItems)

	// Code section: allocate's bump-allocator body, deallocate's no-op
	// body, then one forwarding body per function (local.get 0; local.get
	// 1; call <import index>; end).
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

	codeItems := [][]byte{
		prefixLen(allocateBody),
		prefixLen(deallocateBody),
	}
	for i := range funcNames {
		body := []byte{
			0x00,       // no locals
			0x20, 0x00, // local.get 0
			0x20, 0x01, // local.get 1
			0x10, byte(i), // call <import index i>
			0x0B, // end
		}
		codeItems = append(codeItems, prefixLen(body))
	}
	codeSec := concatVec(codeItems)

	var m []byte
	m = append(m, 0x00, 0x61, 0x73, 0x6D, 0x01, 0x00, 0x00, 0x00) // magic + version
	m = append(m, section(secType, typeSec)...)
	m = append(m, section(secImport, importSec)...)
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
	return encodeVecBytes(uLEB128(uint32(len(name))), []byte(name))
}

func encodeVecBytes(lenPrefix, data []byte) []byte {
	return append(append([]byte{}, lenPrefix...), data...)
}

func bytesRepeat(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
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
