package engine

// EngineResponse is the response envelope writeResponse writes to the
// client — populated by dispatchORMRoute (Table/Transient EnableOps
// routes, via an engineResponseRecorder) or invokeHandler (WASM-backed
// routes) alike, so both dispatch paths produce a byte-identical envelope
// through the one shared writeResponse call.
type EngineResponse struct {
	StatusCode int
	Headers    map[string]string
	Body       []byte
}
