package vaultpki

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fakePKIServer struct {
	mux *http.ServeMux

	lastIssueBody  map[string]any
	lastRevokeBody map[string]any
	revoked        []string
}

func newFakePKIServer(t *testing.T) (*httptest.Server, *fakePKIServer) {
	t.Helper()

	fp := &fakePKIServer{mux: http.NewServeMux()}

	fp.mux.HandleFunc("PUT /v1/pki/issue/operator", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&fp.lastIssueBody)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"certificate":   "-----BEGIN CERTIFICATE-----\nfake-leaf\n-----END CERTIFICATE-----",
				"private_key":   "-----BEGIN PRIVATE KEY-----\nfake-key\n-----END PRIVATE KEY-----",
				"serial_number": "11:22:33:44",
			},
		})
	})
	fp.mux.HandleFunc("PUT /v1/pki/revoke", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&fp.lastRevokeBody)
		if serial, ok := fp.lastRevokeBody["serial_number"].(string); ok {
			fp.revoked = append(fp.revoked, serial)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{}})
	})
	fp.mux.HandleFunc("GET /v1/pki/crl/pem", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-pem-file")
		revokedCount := len(fp.revoked)
		_, _ = w.Write([]byte(fmtCRL(revokedCount)))
	})

	srv := httptest.NewServer(fp.mux)
	t.Cleanup(srv.Close)
	return srv, fp
}

func fmtCRL(revokedCount int) string {
	if revokedCount == 0 {
		return "-----BEGIN X509 CRL-----\nfake-empty-crl\n-----END X509 CRL-----"
	}
	return "-----BEGIN X509 CRL-----\nfake-crl-with-revocations\n-----END X509 CRL-----"
}

func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	t.Setenv("GOERP_VAULT_ADDR", srv.URL)
	t.Setenv("GOERP_VAULT_AUTH_METHOD", "token")
	t.Setenv("GOERP_VAULT_TOKEN", "static-token")

	client, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	return client
}

func TestIssueCert_ReturnsCertKeyAndSerial(t *testing.T) {
	srv, fp := newFakePKIServer(t)
	client := newTestClient(t, srv)

	issued, err := client.IssueCert(context.Background(), "kwame-operator", 90*24*time.Hour)
	if err != nil {
		t.Fatalf("IssueCert() error: %v", err)
	}

	if issued.CertificatePEM == "" || issued.PrivateKeyPEM == "" || issued.SerialNumber == "" {
		t.Fatalf("IssueCert() returned incomplete result: %+v", issued)
	}
	if issued.SerialNumber != "11:22:33:44" {
		t.Errorf("SerialNumber = %q, want %q", issued.SerialNumber, "11:22:33:44")
	}
	if fp.lastIssueBody["common_name"] != "kwame-operator" {
		t.Errorf("request common_name = %v, want %q", fp.lastIssueBody["common_name"], "kwame-operator")
	}
}

func TestRevokeCert_AddsSerialToCRL(t *testing.T) {
	srv, fp := newFakePKIServer(t)
	client := newTestClient(t, srv)

	before, err := client.CRL(context.Background())
	if err != nil {
		t.Fatalf("CRL() error: %v", err)
	}

	if err := client.RevokeCert(context.Background(), "11:22:33:44"); err != nil {
		t.Fatalf("RevokeCert() error: %v", err)
	}
	if len(fp.revoked) != 1 || fp.revoked[0] != "11:22:33:44" {
		t.Errorf("revoked serials = %v, want [11:22:33:44]", fp.revoked)
	}

	after, err := client.CRL(context.Background())
	if err != nil {
		t.Fatalf("CRL() error: %v", err)
	}
	if string(before) == string(after) {
		t.Error("CRL fetched after revocation should differ from the CRL fetched before it")
	}
}

func TestCRL_ReturnsPEMBytes(t *testing.T) {
	srv, _ := newFakePKIServer(t)
	client := newTestClient(t, srv)

	crl, err := client.CRL(context.Background())
	if err != nil {
		t.Fatalf("CRL() error: %v", err)
	}
	if len(crl) == 0 {
		t.Fatal("CRL() returned no bytes")
	}
	if s := string(crl); s[:5] != "-----" {
		t.Errorf("CRL() = %q, want PEM-encoded content", s)
	}
}

func TestIssueCert_UnreachableVaultFails(t *testing.T) {
	t.Setenv("GOERP_VAULT_ADDR", "http://127.0.0.1:1")
	t.Setenv("GOERP_VAULT_AUTH_METHOD", "token")
	t.Setenv("GOERP_VAULT_TOKEN", "static-token")

	client, err := New()
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := client.IssueCert(ctx, "kwame-operator", time.Hour); err == nil {
		t.Fatal("IssueCert() error = nil, want a connection failure")
	}
}
