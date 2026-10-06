package wasm

import (
	"context"
	"errors"
	"fmt"
	"time"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/tetratelabs/wazero"
	"github.com/vmihailenco/msgpack/v5"
)

// WebhookVerifyTimeout bounds one verifier call. The call is made before the
// request is acknowledged, on behalf of an unauthenticated sender
// (host-abi-reference.md §10b).
const WebhookVerifyTimeout = 5 * time.Second

// webhookVerifyTimeout is WebhookVerifyTimeout; tests shorten it.
var webhookVerifyTimeout = WebhookVerifyTimeout

// WebhookOutcome is how a verifier call ended.
type WebhookOutcome int

const (
	// WebhookAccepted: the delivery is authentic and carries a provider event ID.
	WebhookAccepted WebhookOutcome = iota + 1
	// WebhookRejected: the verifier judged the signature invalid.
	WebhookRejected
	// WebhookVerifierError: the verifier errored, panicked, returned valid
	// without an event ID, returned an undecodable response, timed out, or
	// could not be run.
	WebhookVerifierError
	// WebhookNoVerifier: the module exports no handle_webhook_verify.
	WebhookNoVerifier
)

// WebhookVerdict is the result of VerifyWebhook. ProviderEventID is set only
// for WebhookAccepted, and Message only for WebhookVerifierError.
type WebhookVerdict struct {
	Outcome         WebhookOutcome
	ProviderEventID string
	Message         string
}

// WebhookVerifierSource is where VerifyWebhook gets an instance of the
// connector: its pool, or for a disabled module whose pool is drained, a
// transient instance of its compiled module.
type WebhookVerifierSource struct {
	ModuleName string
	Pool       *InstancePool
	Compiled   wazero.CompiledModule
}

// VerifyWebhook runs a connector's webhook verifier on req, with no tenant
// context and only host.crypto available, bounded by WebhookVerifyTimeout.
func (r *Runtime) VerifyWebhook(ctx context.Context, src WebhookVerifierSource, req abiv1.WebhookVerifyRequest) WebhookVerdict {
	ctx, cancel := context.WithTimeout(ctx, webhookVerifyTimeout)
	defer cancel()

	inst, release, err := r.webhookVerifierInstance(ctx, src)
	if err != nil {
		return verifierError("get module instance: %v", err)
	}
	defer release()

	if !inst.HasWebhookVerifier() {
		return WebhookVerdict{Outcome: WebhookNoVerifier}
	}

	modCtx := NewModuleContext("", src.ModuleName, "", "", nil, nil, "", "", "", 0, nil, ModuleSnapshot{})
	modCtx.webhookVerify = true
	inst.SetModuleContext(modCtx)
	r.RegisterInstance(inst)
	defer func() {
		r.UnregisterInstance(inst)
		inst.SetModuleContext(nil)
	}()

	payload, err := msgpack.Marshal(req)
	if err != nil {
		return verifierError("encode request: %v", err)
	}
	raw, err := inst.InvokeHandleWebhookVerify(ctx, payload)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return verifierError("verifier exceeded %s", webhookVerifyTimeout)
		}
		return verifierError("invoke verifier: %v", err)
	}

	var resp abiv1.WebhookVerifyResponse
	if err := msgpack.Unmarshal(raw, &resp); err != nil {
		return verifierError("decode verifier response: %v", err)
	}
	switch {
	case resp.Error != "":
		return WebhookVerdict{Outcome: WebhookVerifierError, Message: resp.Error}
	case !resp.Valid:
		return WebhookVerdict{Outcome: WebhookRejected}
	case resp.ProviderEventID == "":
		return verifierError("verifier accepted the delivery without a provider event ID")
	}
	return WebhookVerdict{Outcome: WebhookAccepted, ProviderEventID: resp.ProviderEventID}
}

func verifierError(format string, args ...any) WebhookVerdict {
	return WebhookVerdict{Outcome: WebhookVerifierError, Message: fmt.Sprintf(format, args...)}
}

// webhookVerifierInstance returns a pooled instance, or a transient one when
// the module has no usable pool. release must be called when done.
func (r *Runtime) webhookVerifierInstance(ctx context.Context, src WebhookVerifierSource) (*ModuleInstance, func(), error) {
	if src.Pool != nil {
		inst, err := src.Pool.Borrow(ctx)
		if err == nil {
			return inst, func() { src.Pool.Return(inst) }, nil
		}
		if !errors.Is(err, ErrPoolDraining) || src.Compiled == nil {
			return nil, nil, err
		}
	}
	if src.Compiled == nil {
		return nil, nil, errors.New("module has neither a pool nor a compiled module")
	}

	inst, err := r.InstantiateTemp(ctx, src.ModuleName, src.Compiled)
	if err != nil {
		return nil, nil, err
	}
	return inst, func() { _ = inst.Module().Close(context.Background()) }, nil
}
