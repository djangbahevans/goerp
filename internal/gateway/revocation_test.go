package gateway

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"math/big"
	"sync"
	"testing"
	"time"
)

type fakeCRLFetcher struct {
	mu      sync.Mutex
	pem     []byte
	err     error
	fetches int
}

func (f *fakeCRLFetcher) CRL(ctx context.Context) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetches++
	if f.err != nil {
		return nil, f.err
	}
	return f.pem, nil
}

func (f *fakeCRLFetcher) set(pemBytes []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pem = pemBytes
}

func (f *fakeCRLFetcher) setErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// buildCRL creates a real, ca-signed CRL PEM revoking the given serials.
func buildCRL(t *testing.T, ca *testCA, serials ...*big.Int) []byte {
	t.Helper()

	entries := make([]x509.RevocationListEntry, len(serials))
	for i, s := range serials {
		entries[i] = x509.RevocationListEntry{SerialNumber: s, RevocationTime: time.Now()}
	}

	template := &x509.RevocationList{
		Number:                    big.NewInt(1),
		ThisUpdate:                time.Now().Add(-time.Minute),
		NextUpdate:                time.Now().Add(time.Hour),
		RevokedCertificateEntries: entries,
	}

	der, err := x509.CreateRevocationList(nil, template, ca.cert, ca.key)
	if err != nil {
		t.Fatalf("create CRL: %v", err)
	}

	return pem.EncodeToMemory(&pem.Block{Type: "X509 CRL", Bytes: der})
}

func TestCRLRevocationChecker_DetectsRevokedSerial(t *testing.T) {
	ca := newTestCA(t)
	_, _, revokedCert := ca.leaf(t, "revoked-operator", false)
	_, _, liveCert := ca.leaf(t, "live-operator", false)

	revokedX509, err := x509.ParseCertificate(revokedCert.Certificate[0])
	if err != nil {
		t.Fatalf("parse revoked cert: %v", err)
	}
	liveX509, err := x509.ParseCertificate(liveCert.Certificate[0])
	if err != nil {
		t.Fatalf("parse live cert: %v", err)
	}

	fetcher := &fakeCRLFetcher{pem: buildCRL(t, ca, revokedX509.SerialNumber)}
	checker := newCRLRevocationChecker(context.Background(), fetcher, time.Hour)

	if !checker.IsRevoked(revokedX509) {
		t.Error("IsRevoked(revoked cert) = false, want true")
	}
	if checker.IsRevoked(liveX509) {
		t.Error("IsRevoked(live cert) = true, want false")
	}
}

func TestCRLRevocationChecker_PicksUpNewRevocationsOnPoll(t *testing.T) {
	ca := newTestCA(t)
	_, _, cert := ca.leaf(t, "operator-a", false)
	x509Cert, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}

	fetcher := &fakeCRLFetcher{pem: buildCRL(t, ca)} // empty CRL initially
	checker := newCRLRevocationChecker(context.Background(), fetcher, 20*time.Millisecond)

	if checker.IsRevoked(x509Cert) {
		t.Fatal("IsRevoked() = true before any revocation, want false")
	}

	fetcher.set(buildCRL(t, ca, x509Cert.SerialNumber))

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if checker.IsRevoked(x509Cert) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("IsRevoked() never became true after the poll picked up the updated CRL")
}

func TestCRLRevocationChecker_FetchFailureKeepsPreviousList(t *testing.T) {
	ca := newTestCA(t)
	_, _, cert := ca.leaf(t, "operator-a", false)
	x509Cert, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}

	fetcher := &fakeCRLFetcher{pem: buildCRL(t, ca, x509Cert.SerialNumber)}
	checker := newCRLRevocationChecker(context.Background(), fetcher, time.Hour)

	if !checker.IsRevoked(x509Cert) {
		t.Fatal("IsRevoked() = false right after construction, want true")
	}

	fetcher.setErr(errors.New("vault unreachable"))
	checker.refresh(context.Background())

	if !checker.IsRevoked(x509Cert) {
		t.Error("IsRevoked() = false after a failed refresh, want true (previous list preserved)")
	}
}
