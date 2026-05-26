// statnive-license — offline Ed25519 JWT license signer.
//
// Run on a trusted operator laptop, never on a production VPS. The
// private key never enters the binary's deployment surface; only the
// 32-byte public key embedded at internal/license/signing.pub matters
// at boot.
//
// Subcommands:
//
//	sign  — produce a JWT-EdDSA license token for a customer
//	pub   — extract the raw 32-byte public key from the priv PEM
//	        (writes the format internal/license/signing.pub expects)
//
// JWT format mirrors internal/license/license.go: RFC 8037 EdDSA,
// header {"alg":"EdDSA","typ":"JWT"}, claims keyed by the same json
// tags as internal/license.Claims. Verified end-to-end by
// cmd/statnive-license/main_test.go's roundtrip case.
package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/statnive/statnive.live/internal/license"
)

// maxPrivPEMBytes mirrors internal/license.maxLicenseFileBytes. PKCS8
// Ed25519 PEMs are ~120 B; 8 KiB is the same defensive ceiling the
// verifier-side uses to defeat a malicious symlink → OOM.
const maxPrivPEMBytes = 8 << 10

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "statnive-license:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr *os.File) error {
	if len(args) == 0 {
		printUsage(stderr)
		return errors.New("subcommand required (sign | pub)")
	}

	switch args[0] {
	case "sign":
		return runSign(args[1:], stdout)
	case "pub":
		return runPub(args[1:], stdout)
	case "-h", "--help", "help":
		printUsage(stdout)
		return nil
	default:
		printUsage(stderr)
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

func printUsage(w *os.File) {
	_, _ = fmt.Fprint(w, `statnive-license — offline Ed25519 JWT license signer.

Usage:
  statnive-license sign --priv=PATH --customer=NAME --site=ID --max=N \
      --features=a,b,c --exp=YYYY-MM-DD [--iat=YYYY-MM-DD] [--out=PATH]
  statnive-license pub  --priv=PATH [--out=PATH]
  statnive-license help

sign flags:
  --priv=PATH       PEM-encoded Ed25519 PKCS8 private key (operator's age vault)
  --customer=NAME   free-form customer identifier ("sampleplatform")
  --site=N          uint32 site_id this license is bound to (0 = wildcard)
  --max=N           advisory max events/day (not enforced in v1)
  --features=LIST   comma-separated feature flags ("dashboard,tracker,geoip-db23")
  --exp=DATE        YYYY-MM-DD or RFC3339 expiry timestamp (UTC)
  --iat=DATE        optional; default = now (UTC)
  --out=PATH        optional; default = stdout

pub flags:
  --priv=PATH       PEM-encoded Ed25519 PKCS8 private key
  --out=PATH        optional; default = stdout
                    write the 32-byte raw public key (matches the
                    internal/license/signing.pub format expected by the
                    binary; copy this file there before tagging a release).

Generate a keypair (one-time, on trusted laptop, NOT via Claude):
  openssl genpkey -algorithm ed25519 -out license-signing.priv.pem
  statnive-license pub --priv=license-signing.priv.pem \
      --out=internal/license/signing.pub
`)
}

// ---- sign ----

func runSign(args []string, stdout *os.File) error {
	fs := flag.NewFlagSet("sign", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	priv := fs.String("priv", "", "PEM path to Ed25519 private key")
	customer := fs.String("customer", "", "customer identifier")
	site := fs.Uint("site", 0, "site_id (uint32)")
	maxEvents := fs.Uint64("max", 0, "advisory max events/day")
	// --features values are opaque operator-meaningful labels. The v1
	// binary records them in the verified Claims but does NOT enforce
	// any specific list at verify time; that's a v1.1 feature-gating
	// item. Use whatever strings make sense for the customer SOW.
	features := fs.String("features", "", "comma-separated feature list (opaque labels; not enforced in v1)")
	exp := fs.String("exp", "", "expiry: YYYY-MM-DD or RFC3339")
	iat := fs.String("iat", "", "issued-at: YYYY-MM-DD or RFC3339 (default now)")
	out := fs.String("out", "", "output path (default stdout)")

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("sign flags: %w", err)
	}

	if strings.TrimSpace(*customer) == "" {
		return errors.New("sign: --customer is required")
	}

	expAt, err := parseDate(*exp)
	if err != nil {
		return fmt.Errorf("sign: --exp: %w", err)
	}

	if expAt.Before(time.Now()) {
		return fmt.Errorf("sign: --exp %s is in the past — refusing to sign", expAt.Format(time.RFC3339))
	}

	var iatAt time.Time
	if strings.TrimSpace(*iat) == "" {
		iatAt = time.Now().UTC()
	} else {
		iatAt, err = parseDate(*iat)
		if err != nil {
			return fmt.Errorf("sign: --iat: %w", err)
		}
	}

	if *site > 0xFFFFFFFF {
		return fmt.Errorf("sign: --site %d overflows uint32", *site)
	}

	privKey, err := requirePriv("sign", *priv)
	if err != nil {
		return err
	}

	claims := license.Claims{
		Customer:     *customer,
		SiteID:       uint32(*site),
		MaxEventsDay: *maxEvents,
		Features:     splitFeatures(*features),
		IssuedAt:     iatAt.Unix(),
		ExpiresAt:    expAt.Unix(),
	}

	token, err := license.Sign(privKey, claims)
	if err != nil {
		return fmt.Errorf("sign: %w", err)
	}

	return writeOutput(token+"\n", *out, stdout)
}

// ---- pub ----

func runPub(args []string, stdout *os.File) error {
	fs := flag.NewFlagSet("pub", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	priv := fs.String("priv", "", "PEM path to Ed25519 private key")
	out := fs.String("out", "", "output path (default stdout)")

	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("pub flags: %w", err)
	}

	privKey, err := requirePriv("pub", *priv)
	if err != nil {
		return err
	}

	pubKey, ok := privKey.Public().(ed25519.PublicKey)
	if !ok {
		return errors.New("pub: extracted public key is not Ed25519")
	}

	// Output format is raw 32 bytes — the same shape go:embed
	// reads into internal/license:embeddedPubKey. Copy the result
	// over internal/license/signing.pub before tagging a release.
	return writeBytes(pubKey, *out, stdout)
}

// requirePriv validates that path is non-empty and loads the Ed25519
// PKCS8 PEM at it. Error messages carry the subcommand name so the
// operator-facing stderr framing in main() reads cleanly.
func requirePriv(sub, path string) (ed25519.PrivateKey, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("%s: --priv is required", sub)
	}

	k, err := loadEd25519PrivPEM(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", sub, err)
	}

	return k, nil
}

// ---- helpers ----

// loadEd25519PrivPEM reads a PEM-encoded PKCS8 Ed25519 private key
// (the format `openssl genpkey -algorithm ed25519` produces).
//
// Multi-block PEMs (e.g. a cert + key concatenated into one file by
// mistake) are rejected with a clear error rather than silently
// signing with whichever block happened to come first.
//
// SECURITY: the parsed key stays in process memory until exit. Go GC
// may copy it; manual zeroing is unreliable. Mitigation =
// short-lived process + operator's age-encrypted at-rest vault.
func loadEd25519PrivPEM(path string) (ed25519.PrivateKey, error) {
	f, err := os.Open(path) //nolint:gosec // operator-supplied priv path is the entire point of this CLI
	if err != nil {
		return nil, fmt.Errorf("open priv %s: %w", path, err)
	}

	defer func() { _ = f.Close() }()

	raw, err := io.ReadAll(io.LimitReader(f, maxPrivPEMBytes))
	if err != nil {
		return nil, fmt.Errorf("read priv %s: %w", path, err)
	}

	block, rest := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("priv %s: no PEM block found", path)
	}

	if len(bytes.TrimSpace(rest)) > 0 {
		return nil, fmt.Errorf("priv %s: PEM has trailing blocks; refuse to guess which key to sign with", path)
	}

	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("priv %s: parse PKCS8: %w", path, err)
	}

	ed, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("priv %s: not an Ed25519 key (got %T)", path, parsed)
	}

	return ed, nil
}

// parseDate accepts either a YYYY-MM-DD calendar date (interpreted at
// 00:00 UTC) or a full RFC3339 timestamp. Calendar shape is the
// operator-friendly default; RFC3339 is for sub-day precision.
func parseDate(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, errors.New("empty date")
	}

	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t.UTC(), nil
	}

	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}

	return time.Time{}, fmt.Errorf("%q: not YYYY-MM-DD or RFC3339", s)
}

// splitFeatures parses a comma-separated feature list, trimming
// whitespace and dropping empty entries. Returns nil for empty input
// so the JSON omitempty path renders cleanly.
func splitFeatures(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))

	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

// writeOutput writes a string to path (creating with 0640) or stdout.
func writeOutput(s, path string, stdout *os.File) error {
	if strings.TrimSpace(path) == "" {
		_, err := fmt.Fprint(stdout, s)

		return err
	}

	// 0600: the JWT is the operator's customer-signing artifact;
	// world-readable would let any local user spoof the customer.
	return os.WriteFile(path, []byte(s), 0o600)
}

// writeBytes writes a byte slice to path or stdout (raw, no newline).
// Used for the pub subcommand so the output matches signing.pub's
// exact 32-byte format with no trailing whitespace.
func writeBytes(b []byte, path string, stdout *os.File) error {
	if strings.TrimSpace(path) == "" {
		_, err := stdout.Write(b)

		return err
	}

	return os.WriteFile(path, b, 0o644) //nolint:gosec // pubkey is world-readable by design (committed to git, embedded in every binary)
}
