package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/statnive/statnive.live/internal/license"
)

// writeTestPrivPEM emits a fresh Ed25519 PKCS8-encoded PEM matching
// what `openssl genpkey -algorithm ed25519` produces. Returns the
// path + the parsed key for cross-checks.
func writeTestPrivPEM(t *testing.T) (string, ed25519.PrivateKey, ed25519.PublicKey) {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}

	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal pkcs8: %v", err)
	}

	block := &pem.Block{Type: "PRIVATE KEY", Bytes: der}
	path := filepath.Join(t.TempDir(), "priv.pem")

	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatalf("write priv PEM: %v", err)
	}

	return path, priv, pub
}

func TestSign_RoundTrip(t *testing.T) {
	t.Parallel()

	privPath, _, pub := writeTestPrivPEM(t)
	outPath := filepath.Join(t.TempDir(), "license.jwt")
	exp := time.Now().AddDate(0, 0, 30).UTC().Format("2006-01-02")

	args := []string{
		"sign",
		"--priv=" + privPath,
		"--customer=sampleplatform",
		"--site=1",
		"--max=10000000",
		"--features=dashboard,tracker,geoip-db23",
		"--exp=" + exp,
		"--out=" + outPath,
	}

	if err := run(args, os.Stdout, os.Stderr); err != nil {
		t.Fatalf("run sign: %v", err)
	}

	tokenBytes, err := os.ReadFile(outPath) //nolint:gosec // test-only tempdir
	if err != nil {
		t.Fatalf("read out: %v", err)
	}

	token := strings.TrimSpace(string(tokenBytes))
	if strings.Count(token, ".") != 2 {
		t.Fatalf("token shape: want 3 b64url segments, got %q", token)
	}

	// Round-trip proof: CLI signs → binary verifies. Catches any
	// drift in the byte-for-byte JWT contract between issuer + verifier.
	claims, err := license.VerifyToken(token, pub, time.Now())
	if err != nil {
		t.Fatalf("license.VerifyToken: %v", err)
	}

	if claims.Customer != "sampleplatform" {
		t.Errorf("Customer = %q, want sampleplatform", claims.Customer)
	}

	if claims.SiteID != 1 {
		t.Errorf("SiteID = %d, want 1", claims.SiteID)
	}

	if claims.MaxEventsDay != 10_000_000 {
		t.Errorf("MaxEventsDay = %d, want 10_000_000", claims.MaxEventsDay)
	}

	if len(claims.Features) != 3 {
		t.Errorf("Features len = %d, want 3", len(claims.Features))
	}
}

func TestSign_MissingPriv(t *testing.T) {
	t.Parallel()

	err := run([]string{
		"sign",
		"--customer=x",
		"--site=1",
		"--exp=" + time.Now().AddDate(0, 0, 1).Format("2006-01-02"),
	}, os.Stdout, os.Stderr)

	if err == nil || !strings.Contains(err.Error(), "--priv") {
		t.Errorf("want --priv error, got %v", err)
	}
}

func TestSign_MissingCustomer(t *testing.T) {
	t.Parallel()

	privPath, _, _ := writeTestPrivPEM(t)

	err := run([]string{
		"sign",
		"--priv=" + privPath,
		"--site=1",
		"--exp=" + time.Now().AddDate(0, 0, 1).Format("2006-01-02"),
	}, os.Stdout, os.Stderr)

	if err == nil || !strings.Contains(err.Error(), "--customer") {
		t.Errorf("want --customer error, got %v", err)
	}
}

func TestSign_PastExp(t *testing.T) {
	t.Parallel()

	privPath, _, _ := writeTestPrivPEM(t)
	past := time.Now().AddDate(0, 0, -1).UTC().Format("2006-01-02")

	err := run([]string{
		"sign",
		"--priv=" + privPath,
		"--customer=x",
		"--site=1",
		"--exp=" + past,
	}, os.Stdout, os.Stderr)

	if err == nil || !strings.Contains(err.Error(), "past") {
		t.Errorf("want past-exp rejection, got %v", err)
	}
}

func TestSign_BadExpFormat(t *testing.T) {
	t.Parallel()

	privPath, _, _ := writeTestPrivPEM(t)

	err := run([]string{
		"sign",
		"--priv=" + privPath,
		"--customer=x",
		"--site=1",
		"--exp=not-a-date",
	}, os.Stdout, os.Stderr)

	if err == nil || !strings.Contains(err.Error(), "RFC3339") {
		t.Errorf("want bad-date rejection, got %v", err)
	}
}

func TestSign_NonEd25519Priv(t *testing.T) {
	t.Parallel()

	// Write a PEM that's syntactically PEM but contains garbage —
	// loadEd25519PrivPEM should fail at PKCS8 parse.
	path := filepath.Join(t.TempDir(), "junk.pem")

	if err := os.WriteFile(path, []byte("-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----\n"), 0o600); err != nil {
		t.Fatalf("write junk pem: %v", err)
	}

	err := run([]string{
		"sign",
		"--priv=" + path,
		"--customer=x",
		"--site=1",
		"--exp=" + time.Now().AddDate(0, 0, 1).Format("2006-01-02"),
	}, os.Stdout, os.Stderr)

	if err == nil || !strings.Contains(err.Error(), "PKCS8") {
		t.Errorf("want PKCS8-parse error, got %v", err)
	}
}

func TestSign_BadIATFormat(t *testing.T) {
	t.Parallel()

	privPath, _, _ := writeTestPrivPEM(t)

	err := run([]string{
		"sign",
		"--priv=" + privPath,
		"--customer=x",
		"--site=1",
		"--exp=" + time.Now().AddDate(0, 0, 1).Format("2006-01-02"),
		"--iat=not-a-date",
	}, os.Stdout, os.Stderr)

	if err == nil || !strings.Contains(err.Error(), "--iat") {
		t.Errorf("want --iat bad-date rejection, got %v", err)
	}
}

func TestSign_SiteOverflow(t *testing.T) {
	t.Parallel()

	privPath, _, _ := writeTestPrivPEM(t)

	err := run([]string{
		"sign",
		"--priv=" + privPath,
		"--customer=x",
		"--site=4294967296", // uint32 max + 1
		"--exp=" + time.Now().AddDate(0, 0, 1).Format("2006-01-02"),
	}, os.Stdout, os.Stderr)

	if err == nil || !strings.Contains(err.Error(), "overflow") {
		t.Errorf("want uint32 overflow rejection, got %v", err)
	}
}

func TestSign_MultiBlockPEM(t *testing.T) {
	t.Parallel()

	// Two valid PEM blocks concatenated — operator pasted a cert + key
	// by mistake. Signer must refuse rather than silently use the first.
	first, _, _ := writeTestPrivPEM(t)
	second, _, _ := writeTestPrivPEM(t)

	firstBytes, err := os.ReadFile(first) //nolint:gosec // test-only tempdir
	if err != nil {
		t.Fatalf("read first: %v", err)
	}

	secondBytes, err := os.ReadFile(second) //nolint:gosec // test-only tempdir
	if err != nil {
		t.Fatalf("read second: %v", err)
	}

	concatPath := filepath.Join(t.TempDir(), "concat.pem")

	if err := os.WriteFile(concatPath, append(firstBytes, secondBytes...), 0o600); err != nil {
		t.Fatalf("write concat: %v", err)
	}

	err = run([]string{
		"sign",
		"--priv=" + concatPath,
		"--customer=x",
		"--site=1",
		"--exp=" + time.Now().AddDate(0, 0, 1).Format("2006-01-02"),
	}, os.Stdout, os.Stderr)

	if err == nil || !strings.Contains(err.Error(), "trailing") {
		t.Errorf("want multi-block PEM rejection, got %v", err)
	}
}

func TestPub_MissingPriv(t *testing.T) {
	t.Parallel()

	err := run([]string{"pub"}, os.Stdout, os.Stderr)
	if err == nil || !strings.Contains(err.Error(), "--priv") {
		t.Errorf("want --priv error, got %v", err)
	}
}

func TestPub_RoundTrip(t *testing.T) {
	t.Parallel()

	privPath, _, pubExpected := writeTestPrivPEM(t)
	outPath := filepath.Join(t.TempDir(), "signing.pub")

	if err := run([]string{
		"pub",
		"--priv=" + privPath,
		"--out=" + outPath,
	}, os.Stdout, os.Stderr); err != nil {
		t.Fatalf("run pub: %v", err)
	}

	got, err := os.ReadFile(outPath) //nolint:gosec // test-only tempdir
	if err != nil {
		t.Fatalf("read out: %v", err)
	}

	if len(got) != ed25519.PublicKeySize {
		t.Errorf("got %d bytes, want %d", len(got), ed25519.PublicKeySize)
	}

	if string(got) != string(pubExpected) {
		t.Error("pub output != expected ed25519 pubkey")
	}
}

func TestUnknownSubcommand(t *testing.T) {
	t.Parallel()

	err := run([]string{"weld"}, os.Stdout, os.Stderr)
	if err == nil || !strings.Contains(err.Error(), "unknown subcommand") {
		t.Errorf("want unknown-subcommand error, got %v", err)
	}
}

func TestNoArgs(t *testing.T) {
	t.Parallel()

	err := run(nil, os.Stdout, os.Stderr)
	if err == nil || !strings.Contains(err.Error(), "subcommand") {
		t.Errorf("want subcommand-required error, got %v", err)
	}
}

func TestSplitFeatures(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"a", []string{"a"}},
		{"a,b,c", []string{"a", "b", "c"}},
		{"a, b ,c ", []string{"a", "b", "c"}},
		{",,a,,", []string{"a"}},
	}

	for _, tc := range cases {
		got := splitFeatures(tc.in)
		if len(got) != len(tc.want) {
			t.Errorf("splitFeatures(%q) len = %d, want %d", tc.in, len(got), len(tc.want))

			continue
		}

		for i, w := range tc.want {
			if got[i] != w {
				t.Errorf("splitFeatures(%q)[%d] = %q, want %q", tc.in, i, got[i], w)
			}
		}
	}
}

func TestParseDate(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in    string
		ok    bool
		check func(t time.Time) bool
	}{
		{"2027-01-15", true, func(t time.Time) bool { return t.Year() == 2027 && t.Month() == 1 && t.Day() == 15 }},
		{"2027-01-15T10:00:00Z", true, func(t time.Time) bool { return t.Year() == 2027 && t.Hour() == 10 }},
		{"not-a-date", false, nil},
		{"", false, nil},
		{"   ", false, nil},
	}

	for _, tc := range cases {
		got, err := parseDate(tc.in)
		if tc.ok && err != nil {
			t.Errorf("parseDate(%q): unexpected err %v", tc.in, err)

			continue
		}

		if !tc.ok && err == nil {
			t.Errorf("parseDate(%q): want err, got nil", tc.in)

			continue
		}

		if tc.ok && tc.check != nil && !tc.check(got) {
			t.Errorf("parseDate(%q) returned %v, check failed", tc.in, got)
		}
	}
}
