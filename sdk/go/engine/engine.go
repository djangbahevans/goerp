package engine

func DispatchRequest(ptr, length uint32) uint64 {
	buf := ReadMem(ptr, length)

	var req Request
	if err := unmarshal(buf, &req); err != nil {
		return WriteResponse(&Response{
			StatusCode: 400,
			Body: map[string]any{
				"error": map[string]any{
					"code":    "engine.invalid_request",
					"message": err.Error(),
				},
			},
		})
	}

	resp := DefaultRouter.Handle(&req)
	return WriteResponse(resp)
}

func WriteResponse(resp *Response) uint64 {
	data, err := marshal(resp)
	if err != nil {
		data, _ = marshal(&Response{
			StatusCode: 500,
			Body: map[string]any{
				"error": map[string]any{
					"code":    "engine.marshal_failed",
					"message": err.Error(),
				},
			},
		})
	}

	ptr := Allocate(uint32(len(data)))
	WriteMem(ptr, data)
	return uint64(ptr)<<32 | uint64(len(data))
}
