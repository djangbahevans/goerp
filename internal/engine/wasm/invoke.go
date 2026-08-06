package wasm

import (
	"context"
	"fmt"

	"github.com/vmihailenco/msgpack/v5"
)

func Invoke[TReq, TResp any](ctx context.Context, r *Runtime, moduleName, fnName string, req TReq) (TResp, error) {
	var zero TResp

	payload, err := msgpack.Marshal(req)
	if err != nil {
		return zero, fmt.Errorf("marshal request: %w", err)
	}

	data, err := r.CallAndRead(ctx, moduleName, fnName, payload)
	if err != nil {
		return zero, err
	}

	var resp TResp
	if err := msgpack.Unmarshal(data, &resp); err != nil {
		return zero, fmt.Errorf("unmarshal response: %w", err)
	}

	return resp, nil
}

func InvokeStatus[TReq any](ctx context.Context, r *Runtime, moduleName, fnName string, req TReq) error {
	payload, err := msgpack.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	status, err := r.Call(ctx, moduleName, fnName, payload)
	if err != nil {
		return err
	}
	if status != 0 {
		return fmt.Errorf("module returned error code %d", status)
	}

	return nil
}
