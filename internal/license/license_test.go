package license

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// signToken builds a JWT-EdDSA token from the given claims using priv.
// Test-only — production signing happens offline via the v1.1 CLI.
func signToken(t *testing.T, priv ed25519.PrivateKey, c Claims) string {
	t.Helper()

	header := `{"alg":"EdDSA","typ":"JWT"}`
	headerB64 := base64.RawURLEncoding.EncodeToString([]byte(header))

	claimsBytes, err := json.Marshal(c)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}

	claimsB64 := base64.RawURLEncoding.EncodeToString(claimsBytes)

	signingInput := headerB64 + "." + claimsB64
	sig := ed25519.Sign(priv, []byte(signingInput))
	sigB64 := base64.RawURLEncoding.EncodeToString(sig)

	return signingInput + "." + sigB64
}

// genKey returns a fresh Ed25519 keypair for one test.
func genKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}

	return pub, priv
}

func TestVerifyToken_Valid(t *testing.T) {
	t.Parallel()

	pub, priv := genKey(t)

	exp := time.Now().Add(30 * 24 * time.Hour).Unix()
	token := signToken(t, priv, Claims{
		Customer:     "sampleplatform",
		SiteID:       1,
		MaxEventsDay: 10_000_000,
		Features:     []string{"dashboard", "tracker"},
		IssuedAt:     time.Now().Unix(),
		ExpiresAt:    exp,
	})

	c, err := verifyToken(token, pub, time.Now())
	if err != nil {
		t.Fatalf("verifyToken: %v", err)
	}

	if c.Customer != "sampleplatform" {
		t.Errorf("Customer = %q, want sampleplatform", c.Customer)
	}

	if c.SiteID != 1 {
		t.Errorf("SiteID = %d, want 1", c.SiteID)
	}

	if c.MaxEventsDay != 10_000_000 {
		t.Errorf("MaxEventsDay = %d, want 10_000_000", c.MaxEventsDay)
	}

	if len(c.Features) != 2 {
		t.Errorf("Features len = %d, want 2", len(c.Features))
	}
}

func TestVerifyToken_Expired(t *testing.T) {
	t.Parallel()

	pub, priv := genKey(t)

	past := time.Now().Add(-1 * time.Hour).Unix()
	token := signToken(t, priv, Claims{
		Customer:  "expired-co",
		ExpiresAt: past,
	})

	_, err := verifyToken(token, pub, time.Now())
	if !errors.Is(err, ErrExpired) {
		t.Errorf("verifyToken on expired token: got %v, want ErrExpired", err)
	}
}

func TestVerifyToken_BadSignature(t *testing.T) {
	t.Parallel()

	pubGood, priv := genKey(t)
	pubAttacker, _ := genKey(t) // verifier holds the wrong key

	token := signToken(t, priv, Claims{
		Customer:  "victim-co",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	})

	_, err := verifyToken(token, pubAttacker, time.Now())
	if !errors.Is(err, ErrInvalidSignature) {
		t.Errorf("verifyToken with wrong pubkey: got %v, want ErrInvalidSignature", err)
	}

	// Sanity: the same token verifies fine against the original key.
	if _, err := verifyToken(token, pubGood, time.Now()); err != nil {
		t.Errorf("verifyToken with correct pubkey unexpectedly failed: %v", err)
	}
}

func TestVerifyToken_TamperedClaims(t *testing.T) {
	t.Parallel()

	pub, priv := genKey(t)

	token := signToken(t, priv, Claims{
		Customer:     "honest-co",
		SiteID:       1,
		MaxEventsDay: 1_000_000,
		ExpiresAt:    time.Now().Add(time.Hour).Unix(),
	})

	// Threat model: an attacker who saw a valid token tries to inflate
	// MaxEventsDay. Ed25519 binds the signature to the literal claims
	// bytes, so any post-sign mutation must fail.
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("token parts = %d, want 3", len(parts))
	}

	evilClaims, _ := json.Marshal(Claims{
		Customer:     "honest-co",
		SiteID:       1,
		MaxEventsDay: 1_000_000_000, // 1000x inflation
		ExpiresAt:    time.Now().Add(time.Hour).Unix(),
	})
	parts[1] = base64.RawURLEncoding.EncodeToString(evilClaims)
	tampered := strings.Join(parts, ".")

	_, err := verifyToken(tampered, pub, time.Now())
	if !errors.Is(err, ErrInvalidSignature) {
		t.Errorf("verifyToken on tampered claims: got %v, want ErrInvalidSignature", err)
	}
}

func TestVerifyToken_Malformed(t *testing.T) {
	t.Parallel()

	pub, _ := genKey(t)

	cases := []struct {
		name  string
		token string
	}{
		{"empty", ""},
		{"two segments", "aaa.bbb"},
		{"four segments", "a.b.c.d"},
		{"bad base64 sig", "aGVsbG8.d29ybGQ.!!!not-base64!!!"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := verifyToken(tc.token, pub, time.Now())
			if !errors.Is(err, ErrMalformed) {
				t.Errorf("verifyToken(%q): got %v, want ErrMalformed", tc.token, err)
			}
		})
	}
}

func TestVerify_EmptyPath(t *testing.T) {
	t.Parallel()

	_, err := Verify("")
	if !errors.Is(err, ErrNoLicense) {
		t.Errorf("Verify(\"\"): got %v, want ErrNoLicense", err)
	}

	_, err = Verify("   ")
	if !errors.Is(err, ErrNoLicense) {
		t.Errorf("Verify(whitespace): got %v, want ErrNoLicense", err)
	}
}

func TestVerify_PlaceholderPubKey(t *testing.T) {
	t.Parallel()

	// The committed pubkey is the all-zero placeholder. Any non-empty
	// path must fail with ErrPlaceholderPubKey before reaching disk.
	tmp := t.TempDir()
	path := filepath.Join(tmp, "license.jwt")

	if err := os.WriteFile(path, []byte("anything"), 0o644); err != nil {
		t.Fatalf("write tmp license: %v", err)
	}

	_, err := Verify(path)
	if !errors.Is(err, ErrPlaceholderPubKey) {
		t.Errorf("Verify with placeholder pubkey: got %v, want ErrPlaceholderPubKey", err)
	}
}

// TestVerify_PlaceholderShortCircuitsBeforeFileIO pins the security
// ordering: when the dev placeholder key is still embedded, Verify
// must reject the call before touching the filesystem — so a
// misbuild can't be papered over by a crafted license at /any/path.
func TestVerify_PlaceholderShortCircuitsBeforeFileIO(t *testing.T) {
	t.Parallel()

	_, err := Verify("/nonexistent/license.jwt")
	if !errors.Is(err, ErrPlaceholderPubKey) {
		t.Errorf("Verify with placeholder + missing file: got %v, want ErrPlaceholderPubKey", err)
	}
}

// TestVerifyFile_FileMissing exercises the file-IO branch directly
// with a real (non-placeholder) keypair so the error path is covered
// regardless of which pubkey ships in main.
func TestVerifyFile_FileMissing(t *testing.T) {
	t.Parallel()

	pub, _ := genKey(t)

	_, err := verifyFile("/nonexistent/license.jwt", pub, time.Now())
	if err == nil {
		t.Fatal("verifyFile missing file: got nil, want open error")
	}

	if errors.Is(err, ErrNoLicense) || errors.Is(err, ErrPlaceholderPubKey) {
		t.Errorf("verifyFile missing file: got sentinel %v, want fs error", err)
	}
}

// TestVerifyFile_OversizeRejected pins the 8 KiB read cap: a file
// larger than the ceiling is truncated, parsed, and fails at
// ErrMalformed (the truncated tail breaks the JWT shape) — not OOM.
func TestVerifyFile_OversizeRejected(t *testing.T) {
	t.Parallel()

	pub, _ := genKey(t)

	tmp := t.TempDir()
	path := filepath.Join(tmp, "huge.jwt")
	big := bytes.Repeat([]byte("A"), maxLicenseFileBytes+1024)

	if err := os.WriteFile(path, big, 0o644); err != nil {
		t.Fatalf("write huge license: %v", err)
	}

	_, err := verifyFile(path, pub, time.Now())
	if !errors.Is(err, ErrMalformed) {
		t.Errorf("verifyFile on oversize garbage: got %v, want ErrMalformed", err)
	}
}

func TestIsPlaceholderKey(t *testing.T) {
	t.Parallel()

	if !isPlaceholderKey(make([]byte, ed25519.PublicKeySize)) {
		t.Error("all-zero pubkey not detected as placeholder")
	}

	pub, _ := genKey(t)
	if isPlaceholderKey(pub) {
		t.Error("real pubkey false-positive as placeholder")
	}

	if isPlaceholderKey([]byte{0, 0, 0}) {
		t.Error("wrong-size pubkey reported as placeholder")
	}
}

func TestVerify_TimingClock(t *testing.T) {
	t.Parallel()

	pub, priv := genKey(t)
	exp := time.Now().Add(1 * time.Hour).Unix()

	token := signToken(t, priv, Claims{
		Customer:  "clock-co",
		ExpiresAt: exp,
	})

	// One second before exp — must pass.
	just := time.Unix(exp-1, 0)
	if _, err := verifyToken(token, pub, just); err != nil {
		t.Errorf("verifyToken 1s before exp: %v", err)
	}

	// At exp — must fail (the >= check).
	at := time.Unix(exp, 0)
	if _, err := verifyToken(token, pub, at); !errors.Is(err, ErrExpired) {
		t.Errorf("verifyToken at exp: got %v, want ErrExpired", err)
	}
}
