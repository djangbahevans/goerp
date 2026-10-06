package wasm

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"strconv"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/abi"
	"github.com/djangbahevans/goerp/internal/engine/connectoringress"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/vmihailenco/msgpack/v5"
)

// ConnectorInbox is the store behind host.connector — satisfied by
// *connectoringress.Store. Every call is scoped to one tenant and module.
type ConnectorInbox interface {
	GetInbox(ctx context.Context, tenantID, moduleName, id string) (connectoringress.InboxRow, error)
	MarkProcessed(ctx context.Context, tenantID, moduleName, id string) error
	MarkFailed(ctx context.Context, tenantID, moduleName, id, reason string) error
}

// SetConnectorInbox wires host.connector's storage layer. Unset,
// host.connector returns abi.unavailable.
func (r *Runtime) SetConnectorInbox(inbox ConnectorInbox) {
	r.connectorInbox = inbox
}

// registerHostConnector attaches host.connector's inbox functions to the
// runtime. They need no capability; access is scoped to the calling module's
// own rows for the calling tenant.
func registerHostConnector(ctx context.Context, rt wazero.Runtime, r *Runtime) error {
	_, err := r.guardedHostModule(rt, "host.connector").
		NewFunctionBuilder().WithFunc(makeConnectorInboxGet(r)).Export("inbox_get").
		NewFunctionBuilder().WithFunc(makeConnectorInboxMarkProcessed(r)).Export("inbox_mark_processed").
		NewFunctionBuilder().WithFunc(makeConnectorInboxMarkFailed(r)).Export("inbox_mark_failed").
		Instantiate(ctx)
	return err
}

// connectorInboxCall decodes the request of an inbox host function and
// returns the store scoped to the caller.
func connectorInboxCall[In any](r *Runtime, m api.Module, ptr, length uint32) (in In, modCtx *ModuleContext, hostErr *abiv1.HostError) {
	modCtx = r.InstanceForModule(m).ModuleContext()
	inputBytes, err := abi.ReadFromModule(m.Memory(), ptr, length)
	if err != nil {
		return in, nil, abi.MemoryFault()
	}
	if err := msgpack.Unmarshal(inputBytes, &in); err != nil {
		return in, nil, abi.DeserializeError(err)
	}
	if r.connectorInbox == nil {
		return in, nil, &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: "no connector inbox store is configured"}
	}
	return in, modCtx, nil
}

func connectorInboxError(err error) *abiv1.HostError {
	if errors.Is(err, connectoringress.ErrInboxNotFound) {
		return &abiv1.HostError{
			Code:    abiv1.ErrCodeConnectorInboxNotFound,
			Message: "no inbox row with that ID for this tenant and module",
		}
	}
	return &abiv1.HostError{Code: abiv1.ErrCodeUnavailable, Message: err.Error(), Retry: true}
}

func makeConnectorInboxGet(r *Runtime) hostFunc {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		allocate := r.InstanceForModule(m).allocate
		in, modCtx, hostErr := connectorInboxCall[abiv1.ConnectorInboxGetInput](r, m, ptr, length)
		if hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}

		row, err := r.connectorInbox.GetInbox(ctx, modCtx.TenantID, modCtx.ModuleName, in.InboxID)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, connectorInboxError(err))
		}
		payload, err := jsonToMsgpack(row.Payload)
		if err != nil {
			return abi.EncodeHostError(ctx, m, allocate, &abiv1.HostError{
				Code: abiv1.ErrCodeUnavailable, Message: "stored inbox payload is not valid JSON: " + err.Error(),
			})
		}

		return abi.WriteToModule(ctx, m, allocate, abiv1.ConnectorInboxGetOutput{
			ID:              row.ID,
			ProviderEventID: row.ProviderEventID,
			Payload:         payload,
			ReceivedAt:      row.ReceivedAt.Unix(),
			Status:          row.Status,
		})
	}
}

func makeConnectorInboxMarkProcessed(r *Runtime) hostFunc {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		allocate := r.InstanceForModule(m).allocate
		in, modCtx, hostErr := connectorInboxCall[abiv1.ConnectorInboxMarkProcessedInput](r, m, ptr, length)
		if hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}
		if err := r.connectorInbox.MarkProcessed(ctx, modCtx.TenantID, modCtx.ModuleName, in.InboxID); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, connectorInboxError(err))
		}
		return abi.WriteToModule(ctx, m, allocate, abiv1.ConnectorInboxMarkProcessedOutput{})
	}
}

func makeConnectorInboxMarkFailed(r *Runtime) hostFunc {
	return func(ctx context.Context, m api.Module, ptr, length uint32) uint64 {
		allocate := r.InstanceForModule(m).allocate
		in, modCtx, hostErr := connectorInboxCall[abiv1.ConnectorInboxMarkFailedInput](r, m, ptr, length)
		if hostErr != nil {
			return abi.EncodeHostError(ctx, m, allocate, hostErr)
		}
		if err := r.connectorInbox.MarkFailed(ctx, modCtx.TenantID, modCtx.ModuleName, in.InboxID, in.Reason); err != nil {
			return abi.EncodeHostError(ctx, m, allocate, connectorInboxError(err))
		}
		return abi.WriteToModule(ctx, m, allocate, abiv1.ConnectorInboxMarkFailedOutput{})
	}
}

// jsonToMsgpack re-encodes a stored JSON document as msgpack. Integral
// numbers become int64 so monetary amounts keep their exact value, which a
// float64 round trip would not for large magnitudes.
func jsonToMsgpack(raw []byte) ([]byte, error) {
	dec := jsontext.NewDecoder(bytes.NewReader(raw))
	value, err := readJSONValue(dec)
	if err != nil {
		return nil, err
	}
	return msgpack.Marshal(value)
}

func readJSONValue(dec *jsontext.Decoder) (any, error) {
	tok, err := dec.ReadToken()
	if err != nil {
		return nil, err
	}
	switch tok.Kind() {
	case 'n':
		return nil, nil
	case 't', 'f':
		return tok.Bool(), nil
	case '"':
		return tok.String(), nil
	case '0':
		text := tok.String()
		if i, err := strconv.ParseInt(text, 10, 64); err == nil {
			return i, nil
		}
		return strconv.ParseFloat(text, 64)
	case '[':
		items := []any{}
		for dec.PeekKind() != ']' {
			item, err := readJSONValue(dec)
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		_, err := dec.ReadToken()
		return items, err
	case '{':
		fields := map[string]any{}
		for dec.PeekKind() != '}' {
			name, err := dec.ReadToken()
			if err != nil {
				return nil, err
			}
			key := name.String()
			item, err := readJSONValue(dec)
			if err != nil {
				return nil, err
			}
			fields[key] = item
		}
		_, err := dec.ReadToken()
		return fields, err
	}
	return nil, fmt.Errorf("unexpected JSON token %q", tok.Kind())
}
