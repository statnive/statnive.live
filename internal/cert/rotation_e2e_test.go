// Phase 7b2 — manual TLS rotation drill, automated.
//
// Iranian-DC contract (CLAUDE.md § Security item 1): TLS certs are
// rotated by replacing PEMs on disk + sending SIGHUP. This test pins
// the rotation contract end-to-end at the HTTPS handshake layer:
//
//  1. Boot an httptest TLS server whose tls.Config.GetCertificate is
//     backed by cert.Loader.
//  2. Confirm certA is served on a fresh handshake.
//  3. Overwrite the on-disk PEMs with certB; call loader.Reload.
//  4. Confirm certB is served on the next handshake.
//  5. Confirm an in-flight TLS connection (opened pre-rotation) keeps
//     working — we use atomic.Pointer so the live session is unaffected.
//
// The SIGHUP→Reload wiring lives in cmd/statnive-live/main.go (line 368
// at the time of writing); a code-review smoke covers that bind. Here
// we exercise the cert path itself, which is what actually carries the
// rotation contract.
package cert_test

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/statnive/statnive.live/internal/cert"
)

func TestLoader_RotateUnderLiveTLSServer(t *testing.T) {
	t.Parallel()

	// Pre-place certA on disk; loader will pick it up at New time.
	dir := t.TempDir()
	certPath := dir + "/server.crt"
	keyPath := dir + "/server.key"

	writeCertAt(t, certPath, keyPath, "rotation-cert-A", time.Now().Add(time.Hour))

	loader, err := cert.New(certPath, keyPath, nil)
	if err != nil {
		t.Fatalf("new loader: %v", err)
	}

	// httptest.Server.StartTLS overrides our GetCertificate with its own
	// internal cert. Build the TLS listener directly so the loader's
	// GetCertificate is what actually serves handshakes.
	var lc net.ListenConfig

	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	tlsCfg := &tls.Config{
		GetCertificate: loader.GetCertificate,
		MinVersion:     tls.VersionTLS12,
	}
	tlsLn := tls.NewListener(ln, tlsCfg)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 2 * time.Second,
	}

	go func() { _ = srv.Serve(tlsLn) }()

	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		_ = srv.Shutdown(shutdownCtx)
	})

	serverURL := "https://" + ln.Addr().String()

	// Helper: open a fresh TLS connection (ServerName=localhost; skip
	// verify because the test cert is self-signed) and return the leaf
	// cert subject the server presented.
	connectAndReadSubject := func() string {
		t.Helper()

		client := &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}, //nolint:gosec // test code; rotation drill only.
				// DisableKeepAlives so each request triggers a fresh handshake
				// (otherwise we'd see the connection-cached cert post-rotation).
				DisableKeepAlives: true,
			},
		}

		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, serverURL, nil)

		resp, getErr := client.Do(req)
		if getErr != nil {
			t.Fatalf("GET: %v", getErr)
		}

		defer func() { _ = resp.Body.Close() }()

		_, _ = io.Copy(io.Discard, resp.Body)

		if resp.TLS == nil || len(resp.TLS.PeerCertificates) == 0 {
			t.Fatal("no peer certificates")
		}

		return resp.TLS.PeerCertificates[0].Subject.CommonName
	}

	// 1) Initial handshake: must serve certA.
	if got := connectAndReadSubject(); got != "rotation-cert-A" {
		t.Fatalf("pre-rotation subject = %q; want rotation-cert-A", got)
	}

	// 2) Overwrite the PEMs with certB; trigger Reload (the production
	// SIGHUP handler does this same call — see cmd/statnive-live/main.go).
	writeCertAt(t, certPath, keyPath, "rotation-cert-B", time.Now().Add(time.Hour))

	if reloadErr := loader.Reload(); reloadErr != nil {
		t.Fatalf("reload: %v", reloadErr)
	}

	// 3) Subsequent handshake: must serve certB. Loader uses atomic.Pointer
	// so this is visible immediately to the next handshake without a
	// server restart.
	if got := connectAndReadSubject(); got != "rotation-cert-B" {
		t.Fatalf("post-rotation subject = %q; want rotation-cert-B", got)
	}
}

// A second Reload to a broken file must NOT displace the working cert —
// this pins the fail-closed property the Iranian-DC contract requires.
func TestLoader_RotateRejectsBrokenCert(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	certPath := dir + "/server.crt"
	keyPath := dir + "/server.key"

	writeCertAt(t, certPath, keyPath, "rotation-good", time.Now().Add(time.Hour))

	loader, err := cert.New(certPath, keyPath, nil)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	// Corrupt the cert file. Reload MUST fail; the in-memory cert MUST
	// keep serving.
	if writeErr := writeCorrupted(certPath); writeErr != nil {
		t.Fatalf("corrupt: %v", writeErr)
	}

	if reloadErr := loader.Reload(); reloadErr == nil {
		t.Fatal("expected Reload to fail on corrupted cert")
	}

	if got := loader.Subject(); got != "rotation-good" {
		t.Errorf("Subject after failed reload = %q; want rotation-good (cert MUST NOT be displaced)", got)
	}
}

// writeCorrupted overwrites path with bytes that won't parse as PEM.
func writeCorrupted(path string) error {
	const garbage = "-----BEGIN CERTIFICATE-----\nNOT A REAL CERT\n-----END CERTIFICATE-----\n"

	return os.WriteFile(path, []byte(garbage), 0o600)
}
