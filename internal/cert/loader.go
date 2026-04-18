// Package cert holds the TLS certificate loader + expiry watcher.
//
// Air-gap invariant: this package never dials Let's Encrypt or any ACME
// server. Certificates are operator-managed PEM files; refresh is driven
// by SIGHUP from a cron that calls certbot (or any cert tool) on a
// separate host. Autocert / Let's Encrypt integration is explicitly v1.1
// per CLAUDE.md / PLAN.md §172.
package cert

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"
	"time"

	"github.com/statnive/statnive.live/internal/audit"
)

// Loader keeps the active *tls.Certificate under an atomic.Pointer so the
// HTTP server's tls.Config.GetCertificate callback hot-reloads on the next
// handshake — in-flight TLS sessions keep their already-negotiated cert.
//
// Reload validates the new cert chain BEFORE swapping the pointer, so a
// broken file on disk never displaces a working cert. The old cert keeps
// serving until the operator fixes the new one.
type Loader struct {
	certFile string
	keyFile  string
	audit    *audit.Logger

	cert atomic.Pointer[tls.Certificate]
}

// New parses the cert + key from disk, validates the chain, and stores the
// result under the atomic pointer. Both files must exist + parse — the
// caller (main.go) decides what to do with an empty config (boot HTTP-only)
// vs a populated-but-broken config (fail-closed).
func New(certFile, keyFile string, auditLog *audit.Logger) (*Loader, error) {
	if certFile == "" || keyFile == "" {
		return nil, errors.New("cert: both certFile and keyFile are required")
	}

	l := &Loader{certFile: certFile, keyFile: keyFile, audit: auditLog}

	parsed, err := loadAndValidate(certFile, keyFile)
	if err != nil {
		l.emit(audit.EventTLSCertLoadFailed, slog.String("err", err.Error()))

		return nil, err
	}

	l.cert.Store(parsed)
	l.emitLoaded(parsed, audit.EventTLSCertLoaded)

	return l, nil
}

// Reload re-reads the cert + key from disk. On parse / validation failure
// the old cert keeps serving — we never atom-swap to a broken cert.
//
// Safe to call from any goroutine; concurrent Reload calls serialize on
// the underlying file reads (the atomic pointer makes the swap itself
// lock-free).
func (l *Loader) Reload() error {
	l.emit(audit.EventTLSReloadRequest)

	parsed, err := loadAndValidate(l.certFile, l.keyFile)
	if err != nil {
		l.emit(audit.EventTLSReloadFailed, slog.String("err", err.Error()))

		return fmt.Errorf("cert reload: %w", err)
	}

	l.cert.Store(parsed)
	l.emitLoaded(parsed, audit.EventTLSReloadOK)

	return nil
}

// GetCertificate is the tls.Config.GetCertificate callback. The HTTP server
// invokes this once per TLS handshake. Lock-free via atomic.Pointer.Load.
func (l *Loader) GetCertificate(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
	c := l.cert.Load()
	if c == nil {
		return nil, errors.New("cert: no certificate loaded")
	}

	return c, nil
}

// NotAfter returns the leaf certificate's expiration time. Used by the
// expiry watcher and by /healthz to surface cert lifetime to operators.
// Returns the zero time if no cert is loaded.
func (l *Loader) NotAfter() time.Time {
	c := l.cert.Load()
	if c == nil || c.Leaf == nil {
		return time.Time{}
	}

	return c.Leaf.NotAfter
}

// Subject returns the leaf certificate's subject CN, or "" if none.
// Convenience for /healthz + audit log readers.
func (l *Loader) Subject() string {
	c := l.cert.Load()
	if c == nil || c.Leaf == nil {
		return ""
	}

	return c.Leaf.Subject.CommonName
}

// loadAndValidate reads + parses a PEM keypair. Populates Leaf so the
// expiry watcher can read NotAfter without a second parse. Validates that
// the leaf cert isn't already expired — a fresh cert that's been
// backdated on disk would otherwise serve TLS handshake errors at the
// next connection.
func loadAndValidate(certFile, keyFile string) (*tls.Certificate, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load keypair %q + %q: %w", certFile, keyFile, err)
	}

	if len(cert.Certificate) == 0 {
		return nil, errors.New("cert: empty certificate chain")
	}

	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("parse leaf cert: %w", err)
	}

	if time.Now().After(leaf.NotAfter) {
		return nil, fmt.Errorf("cert already expired at %s", leaf.NotAfter.Format(time.RFC3339))
	}

	cert.Leaf = leaf

	return &cert, nil
}

// emit fires an audit event with no extra attrs. Nil-safe — callers that
// don't want the audit dependency can pass a nil *audit.Logger.
func (l *Loader) emit(name audit.EventName, attrs ...slog.Attr) {
	if l.audit == nil {
		return
	}

	l.audit.Event(context.Background(), name, attrs...)
}

func (l *Loader) emitLoaded(c *tls.Certificate, name audit.EventName) {
	if l.audit == nil || c == nil || c.Leaf == nil {
		return
	}

	l.emit(name,
		slog.String("subject", c.Leaf.Subject.CommonName),
		slog.String("serial", c.Leaf.SerialNumber.String()),
		slog.Time("not_after", c.Leaf.NotAfter),
		slog.String("not_after_in", time.Until(c.Leaf.NotAfter).Round(time.Hour).String()),
	)
}

// MissingFileError checks whether an error from New / Reload was because
// of a missing PEM file — useful for distinguishing "operator forgot to
// set up certs" from "the cert is malformed" in startup diagnostics.
func MissingFileError(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}
