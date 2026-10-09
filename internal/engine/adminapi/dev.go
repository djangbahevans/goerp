package adminapi

import (
	"context"
	"encoding/json/jsontext"
	"fmt"
	"io"
	"net/http"
)

type DevSession struct {
	TenantID string `json:"tenant_id"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type DevBootstrapper interface {
	BootstrapDev(context.Context, string) (DevSession, error)
}

type DevSeedBatch struct {
	Model   string                      `json:"model"`
	Records []map[string]jsontext.Value `json:"records"`
}

type DevSeeder interface {
	SeedDev(context.Context, string, []DevSeedBatch) error
}

func RegisterDevRoute(mux *http.ServeMux, enabled bool, environment, domain string, bootstrapper DevBootstrapper) {
	if !enabled || environment != "development" || domain != "localhost" {
		return
	}

	mux.HandleFunc("POST /admin/dev/bootstrap", func(w http.ResponseWriter, r *http.Request) {
		req, err := decodeJSON[struct {
			Module string `json:"module"`
		}](r)
		if err != nil || req.Module == "" {
			writeError(w, http.StatusBadRequest, "invalid_request", "module is required")
			return
		}

		result, err := bootstrapper.BootstrapDev(r.Context(), req.Module)
		if err != nil {
			writeError(w, http.StatusConflict, "dev_bootstrap_failed", err.Error())
			return
		}

		w.Header().Set("Cache-Control", "no-store")
		writeData(w, http.StatusOK, result)
	})

	if seeder, ok := bootstrapper.(DevSeeder); ok {
		mux.HandleFunc("POST /admin/dev/seed", func(w http.ResponseWriter, r *http.Request) {
			req, err := decodeJSON[struct {
				Module  string         `json:"module"`
				Batches []DevSeedBatch `json:"batches"`
			}](r)
			if err != nil || req.Module == "" {
				writeError(w, http.StatusBadRequest, "invalid_request", "module and valid seed batches are required")
				return
			}

			if err := seeder.SeedDev(r.Context(), req.Module, req.Batches); err != nil {
				writeError(w, http.StatusConflict, "dev_seed_failed", err.Error())
				return
			}

			writeData(w, http.StatusOK, struct{}{})
		})
	}
}

func RegisterDevReloadRoute(mux *http.ServeMux, enabled bool, environment, domain string, reload func(context.Context, string, []byte) error) {
	if !enabled || environment != "development" || domain != "localhost" {
		return
	}

	mux.HandleFunc("POST /admin/dev/modules/{name}/reload", func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(r.Body)
		if err != nil || len(data) == 0 {
			writeError(w, http.StatusBadRequest, "invalid_request", "a module package is required")
			return
		}

		if err := reload(r.Context(), r.PathValue("name"), data); err != nil {
			writeError(w, http.StatusConflict, "dev_reload_failed", fmt.Sprintf("reload module: %v", err))
			return
		}

		writeData(w, http.StatusOK, struct{}{})
	})
}
