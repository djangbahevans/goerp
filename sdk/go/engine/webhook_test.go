package engine

import (
	"errors"
	"net/http"
	"testing"

	abi "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/vmihailenco/msgpack/v5"
)

func withFreshWebhookVerifier(t *testing.T) {
	t.Helper()
	orig := webhookVerifier
	webhookVerifier = nil
	t.Cleanup(func() { webhookVerifier = orig })
}

func dispatchWebhookVerify(t *testing.T, req abi.WebhookVerifyRequest) abi.WebhookVerifyResponse {
	t.Helper()
	data, err := marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	ptr := Allocate(uint32(len(data)))
	WriteMem(ptr, data)

	packed := DispatchWebhookVerify(ptr, uint32(len(data)))
	var resp abi.WebhookVerifyResponse
	if err := msgpack.Unmarshal(ReadMem(uint32(packed>>32), uint32(packed)), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	return resp
}

func TestRegisterWebhookVerifier_PanicsOnNilAndDuplicate(t *testing.T) {
	withFreshWebhookVerifier(t)
	verifier := func([][]byte, http.Header, []byte) (bool, string, error) { return true, "e", nil }

	assertPanics(t, "nil verifier", func() { RegisterWebhookVerifier(nil) })
	RegisterWebhookVerifier(verifier)
	assertPanics(t, "second verifier", func() { RegisterWebhookVerifier(verifier) })
}

func assertPanics(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s did not panic", name)
		}
	}()
	fn()
}

func TestDispatchWebhookVerify_PassesRequestToVerifier(t *testing.T) {
	withFreshWebhookVerifier(t)
	var gotSecrets [][]byte
	var gotHeader string
	var gotBody []byte
	RegisterWebhookVerifier(func(secrets [][]byte, headers http.Header, rawBody []byte) (bool, string, error) {
		gotSecrets, gotHeader, gotBody = secrets, headers.Get("X-Signature"), rawBody
		return true, "evt_1", nil
	})

	resp := dispatchWebhookVerify(t, abi.WebhookVerifyRequest{
		Secrets: [][]byte{[]byte("current"), []byte("previous")},
		Headers: map[string][]string{"X-Signature": {"abc"}},
		Body:    []byte(`{"id":"evt_1"}`),
	})

	if !resp.Valid || resp.ProviderEventID != "evt_1" || resp.Error != "" {
		t.Errorf("response = %+v, want valid with evt_1", resp)
	}
	if len(gotSecrets) != 2 || string(gotSecrets[0]) != "current" || string(gotSecrets[1]) != "previous" {
		t.Errorf("secrets = %q, want current then previous", gotSecrets)
	}
	if gotHeader != "abc" || string(gotBody) != `{"id":"evt_1"}` {
		t.Errorf("header %q body %q not passed through", gotHeader, gotBody)
	}
}

func TestDispatchWebhookVerify_Outcomes(t *testing.T) {
	tests := []struct {
		name     string
		register WebhookVerifier
		want     abi.WebhookVerifyResponse
	}{
		{"invalid signature", func([][]byte, http.Header, []byte) (bool, string, error) { return false, "", nil },
			abi.WebhookVerifyResponse{}},
		{"verifier error", func([][]byte, http.Header, []byte) (bool, string, error) { return false, "", errors.New("bad body") },
			abi.WebhookVerifyResponse{Error: "bad body"}},
		{"error wins over valid", func([][]byte, http.Header, []byte) (bool, string, error) { return true, "e", errors.New("late") },
			abi.WebhookVerifyResponse{Error: "late"}},
		{"verifier panic", func([][]byte, http.Header, []byte) (bool, string, error) { panic("boom") },
			abi.WebhookVerifyResponse{Error: "verifier panicked: boom"}},
		{"no verifier registered", nil,
			abi.WebhookVerifyResponse{Error: "no webhook verifier registered"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withFreshWebhookVerifier(t)
			if tt.register != nil {
				RegisterWebhookVerifier(tt.register)
			}
			if got := dispatchWebhookVerify(t, abi.WebhookVerifyRequest{}); got != tt.want {
				t.Errorf("response = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestDispatchWebhookVerify_UndecodableRequestIsAnErrorResponse(t *testing.T) {
	withFreshWebhookVerifier(t)
	RegisterWebhookVerifier(func([][]byte, http.Header, []byte) (bool, string, error) { return true, "e", nil })

	ptr := Allocate(3)
	WriteMem(ptr, []byte{0xc1, 0xc1, 0xc1})
	packed := DispatchWebhookVerify(ptr, 3)

	var resp abi.WebhookVerifyResponse
	if err := msgpack.Unmarshal(ReadMem(uint32(packed>>32), uint32(packed)), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Valid || resp.Error == "" {
		t.Errorf("response = %+v, want an error and not valid", resp)
	}
}

func TestDispatchWebhookVerify_CanonicalizesHeaderNames(t *testing.T) {
	withFreshWebhookVerifier(t)
	var got string
	RegisterWebhookVerifier(func(_ [][]byte, headers http.Header, _ []byte) (bool, string, error) {
		got = headers.Get("X-Paystack-Signature")
		return true, "e", nil
	})

	dispatchWebhookVerify(t, abi.WebhookVerifyRequest{Headers: map[string][]string{"x-paystack-signature": {"sig"}}})

	if got != "sig" {
		t.Errorf("Get(\"X-Paystack-Signature\") = %q, want sig for a lowercase header name", got)
	}
}
