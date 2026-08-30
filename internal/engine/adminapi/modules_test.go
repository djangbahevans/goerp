package adminapi

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/djangbahevans/goerp/internal/engine/moduleinstall"
)

type fakeModuleInstaller struct {
	gotPkg []byte
	jobID  string
	err    error
}

func (f *fakeModuleInstaller) StartInstall(ctx context.Context, pkg []byte) (string, error) {
	f.gotPkg = pkg
	return f.jobID, f.err
}

func TestModuleInstallRoute_Success(t *testing.T) {
	fake := &fakeModuleInstaller{jobID: "job_abc123"}
	mux := http.NewServeMux()
	RegisterModuleRoutes(mux, ModulesDeps{Install: fake})

	body := []byte("PK\x03\x04fake-erp-bytes")
	req := httptest.NewRequest(http.MethodPost, "/admin/modules/install", bytes.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if !bytes.Equal(fake.gotPkg, body) {
		t.Errorf("StartInstall called with %q, want %q", fake.gotPkg, body)
	}
}

func TestModuleInstallRoute_EmptyBodyReturns400(t *testing.T) {
	fake := &fakeModuleInstaller{}
	mux := http.NewServeMux()
	RegisterModuleRoutes(mux, ModulesDeps{Install: fake})

	req := httptest.NewRequest(http.MethodPost, "/admin/modules/install", bytes.NewReader(nil))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestModuleInstallRoute_RegistryRefNotYetSupported(t *testing.T) {
	fake := &fakeModuleInstaller{}
	mux := http.NewServeMux()
	RegisterModuleRoutes(mux, ModulesDeps{Install: fake})

	req := httptest.NewRequest(http.MethodPost, "/admin/modules/install", bytes.NewReader([]byte(`{"registry_ref": "contacts@1.3.0"}`)))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501", w.Code)
	}
}

// TestModuleInstallRoute_ErrorClassification confirms StartInstall's two
// error categories map to different HTTP statuses — moduleinstall.
// ErrInvalidPackage (a malformed package, genuine client input error) to
// 400, and anything else (a persist-to-disk or job-enqueue failure,
// genuine infra errors) to 500, matching every other adminapi handler's
// convention of only using 400 for the caller's own mistake.
func TestModuleInstallRoute_ErrorClassification(t *testing.T) {
	tests := map[string]struct {
		err  error
		want int
	}{
		"invalid package is a client error": {
			fmt.Errorf("%w: open package: not a zip", moduleinstall.ErrInvalidPackage),
			http.StatusBadRequest,
		},
		"persist/enqueue failure is an internal error": {
			fmt.Errorf("persist package: disk full"),
			http.StatusInternalServerError,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			fake := &fakeModuleInstaller{err: tt.err}
			mux := http.NewServeMux()
			RegisterModuleRoutes(mux, ModulesDeps{Install: fake})

			req := httptest.NewRequest(http.MethodPost, "/admin/modules/install", bytes.NewReader([]byte("PK\x03\x04fake")))
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)

			if w.Code != tt.want {
				t.Errorf("status = %d, want %d, body = %s", w.Code, tt.want, w.Body.String())
			}
		})
	}
}
