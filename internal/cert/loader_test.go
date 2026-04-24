package cert_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/statnive/statnive.live/internal/cert"
)

func TestLoader_New_LoadsValidCert(t *testing.T) {
	t.Parallel()

	certPath, keyPath := writeCert(t, "loader-test", time.Now().Add(365*24*time.Hour))

	l, err := cert.New(certPath, keyPath, nil)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	if got := l.Subject(); got != "loader-test" {
		t.Errorf("Subject() = %q, want loader-test", got)
	}

	if l.NotAfter().Before(time.Now()) {
		t.Errorf("NotAfter %s is in the past", l.NotAfter())
	}
}

func TestLoader_New_FailsClosedOnMissingFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	_, err := cert.New(filepath.Join(dir, "missing.crt"), filepath.Join(dir, "missing.key"), nil)
	if err == nil {
		t.Fatal("expected error for missing file")
	}

	if !cert.MissingFileError(err) {
		t.Errorf("expected MissingFileError, got %v", err)
	}
}

func TestLoader_New_FailsClosedOnExpiredCert(t *testing.T) {
	t.Parallel()

	certPath, keyPath := writeCert(t, "expired", time.Now().Add(-24*time.Hour))

	_, err := cert.New(certPath, keyPath, nil)
	if err == nil {
		t.Fatal("expected error for expired cert")
	}
}

func TestLoader_New_RejectsEmptyPaths(t *testing.T) {
	t.Parallel()

	_, err := cert.New("", "key.pem", nil)
	if err == nil {
		t.Error("expected error for empty cert path")
	}

	_, err = cert.New("cert.pem", "", nil)
	if err == nil {
		t.Error("expected error for empty key path")
	}
}

func TestLoader_Reload_PicksUpNewCert(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	certPath := filepath.Join(dir, "test.crt")
	keyPath := filepath.Join(dir, "test.key")

	writeCertAt(t, certPath, keyPath, "version-a", time.Now().Add(365*24*time.Hour))

	l, err := cert.New(certPath, keyPath, nil)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	if got := l.Subject(); got != "version-a" {
		t.Fatalf("initial Subject() = %q, want version-a", got)
	}

	writeCertAt(t, certPath, keyPath, "version-b", time.Now().Add(365*24*time.Hour))

	if err := l.Reload(); err != nil {
		t.Fatalf("reload: %v", err)
	}

	if got := l.Subject(); got != "version-b" {
		t.Errorf("post-reload Subject() = %q, want version-b", got)
	}
}

func TestLoader_Reload_KeepsOldCertOnFailure(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	certPath := filepath.Join(dir, "test.crt")
	keyPath := filepath.Join(dir, "test.key")

	writeCertAt(t, certPath, keyPath, "good", time.Now().Add(365*24*time.Hour))

	l, err := cert.New(certPath, keyPath, nil)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	// Replace the cert file with garbage.
	if err := os.WriteFile(certPath, []byte("not a pem"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := l.Reload(); err == nil {
		t.Fatal("expected reload to fail")
	}

	if got := l.Subject(); got != "good" {
		t.Errorf("Subject() = %q after failed reload, want good (original cert)", got)
	}
}

func TestLoader_GetCertificate_HandshakeReady(t *testing.T) {
	t.Parallel()

	certPath, keyPath := writeCert(t, "handshake", time.Now().Add(24*time.Hour))

	l, err := cert.New(certPath, keyPath, nil)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	got, err := l.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}

	if got == nil || got.Leaf == nil || got.Leaf.Subject.CommonName != "handshake" {
		t.Errorf("GetCertificate returned unexpected cert: %+v", got)
	}
}

func TestMissingFileError(t *testing.T) {
	t.Parallel()

	if cert.MissingFileError(nil) {
		t.Error("MissingFileError(nil) should be false")
	}

	if !cert.MissingFileError(os.ErrNotExist) {
		t.Error("MissingFileError(os.ErrNotExist) should be true")
	}

	if cert.MissingFileError(errors.New("other")) {
		t.Error("MissingFileError(arbitrary) should be false")
	}
}

// writeCert writes a fresh self-signed ECDSA P-256 keypair to a temp dir
// and returns (certPath, keyPath). NotAfter controls expiry; pass a past
// time to test fail-closed behavior.
func writeCert(t *testing.T, cn string, notAfter time.Time) (certPath, keyPath string) {
	t.Helper()

	dir := t.TempDir()
	certPath = filepath.Join(dir, "test.crt")
	keyPath = filepath.Join(dir, "test.key")

	writeCertAt(t, certPath, keyPath, cn, notAfter)

	return certPath, keyPath
}

func writeCertAt(t *testing.T, certPath, keyPath, cn string, notAfter time.Time) {
	t.Helper()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}

	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}

	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
}
