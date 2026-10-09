package engine

import (
	"context"
	"crypto/sha256"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"uuid"

	abiv1 "github.com/djangbahevans/goerp/contract/abi/v1"
	"github.com/djangbahevans/goerp/internal/engine/loader"
	"github.com/djangbahevans/goerp/internal/engine/module"
	"github.com/djangbahevans/goerp/internal/engine/wasm"
	"github.com/djangbahevans/goerp/sdk/go/model"
	"github.com/djangbahevans/goerp/sdk/go/perm"
)

func TestDispatchWASMRoute_TransitionOverrideStateGate(t *testing.T) {
	f := newDispatchWorkflowFixture(t)
	path := filepath.Join(t.TempDir(), "workflow.wasm")
	cmd := exec.CommandContext(t.Context(), "go", "build", "-buildmode=c-shared", "-o", path, "./loader/testdata/workflowfixture")
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build workflow fixture: %v\n%s", err, output)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	manifestBytes, err := json.Marshal(map[string]any{
		"name":         "sales",
		"display_name": "Sales",
		"type":         "domain",
		"version":      "1.0.0",
		"description":  "Workflow fixture",
		"abi_version":  "1",
		"engine":       ">=0.5.0 <1.0.0",
		"depends_on":   []string{},
		"capabilities": []string{"db.read", "db.write"},
		"schema":       map[string]any{"owned_models": []string{}},
		"checksum":     fmt.Sprintf("sha256:%x", sum),
		"permissions":  []map[string]any{{"name": "sales:order:confirm", "label": "Confirm order"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	loaded := loader.LoadModule(t.Context(), f.e.wasmRuntime, wasm.PoolConfig{MaxSize: 1, BorrowTimeout: time.Second}, loader.Source{
		Name:          "sales",
		WasmBytes:     data,
		ManifestBytes: manifestBytes,
	})
	if loaded.Status == module.StatusFailed {
		t.Fatal(loaded.FailureReason)
	}
	loaded.Status = module.StatusReady
	t.Cleanup(func() { loaded.Pool.DrainAndClose(context.Background(), 5*time.Second) })
	snap, err := f.e.moduleRegistry.Update(map[string]*module.LoadedModule{"sales": loaded})
	if err != nil {
		t.Fatal(err)
	}
	id := f.createOrder(t)
	handler := f.e.buildDispatchHandler(nil)

	for _, tc := range []struct {
		name    string
		state   string
		missing bool
		status  int
		code    string
	}{
		{name: "eligible", state: "draft", status: http.StatusAccepted},
		{name: "wrong state", state: "confirmed", status: http.StatusConflict, code: abiv1.ErrCodeInvalidTransition},
		{name: "missing record", state: "draft", missing: true, status: http.StatusNotFound, code: abiv1.ErrCodeNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := f.e.primaryDB.ExecContext(t.Context(), "UPDATE tenant_"+f.slug+".order SET state = $1 WHERE id = $2", tc.state, id); err != nil {
				t.Fatal(err)
			}
			requestID := id
			if tc.missing {
				requestID = uuid.New().String()
			}
			url := "/sales/orders/" + requestID + "/confirm"
			entry, params, _, _ := snap.RouteTable().Lookup(http.MethodPost, url)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, f.request(http.MethodPost, url, []byte("invalid JSON"), entry, params))
			if w.Code != tc.status {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if tc.code != "" && decodeErrorCode(t, w) != tc.code {
				t.Fatalf("body=%s, want %s", w.Body.String(), tc.code)
			}
			if tc.status == http.StatusAccepted && !strings.Contains(w.Body.String(), "confirmOrder") {
				t.Fatalf("custom handler did not answer: %s", w.Body.String())
			}

			var state string
			if err := f.e.primaryDB.QueryRowContext(t.Context(), "SELECT state FROM tenant_"+f.slug+".order WHERE id = $1", id).Scan(&state); err != nil {
				t.Fatal(err)
			}
			if state != tc.state {
				t.Fatalf("override performed an automatic state write: state=%q, want %q", state, tc.state)
			}
		})
	}

	if _, err := f.e.primaryDB.ExecContext(t.Context(), "UPDATE tenant_"+f.slug+".order SET state = 'confirmed' WHERE id = $1", id); err != nil {
		t.Fatal(err)
	}
	url := "/sales/orders/" + id + "/cancel"
	entry, params, _, _ := snap.RouteTable().Lookup(http.MethodPost, url)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, f.request(http.MethodPost, url, nil, entry, params))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"state":"cancelled"`) {
		t.Fatalf("native transition status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestBuildChain_TransitionOverrideRequiresDeclaredPermission(t *testing.T) {
	f := newChainFixture(t)
	loaded := *f.reg.Snapshot().Modules()["widgets"]
	transition := model.Transition("draft", "confirmed", "confirm").Requires(perm.Ref(chainTestUngrantedPermission))
	loaded.ModelDecls = []model.ModelDeclaration{*model.Define("order").Field("state", model.Selection("draft", "confirmed").Workflow(transition))}
	loaded.ExplicitRoutes = []abiv1.RouteDeclaration{{
		Model:      "widgets.order",
		Name:       "confirm",
		Transition: &abiv1.WorkflowTransitionRef{From: transition.From, To: transition.To},
	}}
	if _, err := f.reg.Update(map[string]*module.LoadedModule{"widgets": &loaded}); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/widgets/orders/"+uuid.New().String()+"/confirm", nil)
	request.Host = f.domain
	request.Header.Set("Authorization", "Bearer "+f.issueToken(t))
	w := httptest.NewRecorder()
	f.chain(nil).ServeHTTP(w, request)
	if w.Code != http.StatusForbidden || decodeErrorCode(t, w) != "permission_denied" {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}
