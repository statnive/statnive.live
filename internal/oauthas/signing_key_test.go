//go:build chatgpt_app

package oauthas

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func genKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()

	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}

	return k
}

// writeKeyPEM writes a private key (PKCS#1 or PKCS#8) to a file with the given
// perms and returns the path.
func writeKeyPEM(t *testing.T, k *rsa.PrivateKey, pkcs8 bool, perm os.FileMode) string {
	t.Helper()

	var block *pem.Block

	if pkcs8 {
		der, err := x509.MarshalPKCS8PrivateKey(k)
		if err != nil {
			t.Fatalf("marshal pkcs8: %v", err)
		}

		block = &pem.Block{Type: "PRIVATE KEY", Bytes: der}
	} else {
		block = &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}
	}

	path := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), perm); err != nil {
		t.Fatalf("write key: %v", err)
	}

	// os.WriteFile honors umask; force the exact perms we are testing.
	if err := os.Chmod(path, perm); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	return path
}

func writePubPEM(t *testing.T, pub *rsa.PublicKey) string {
	t.Helper()

	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("marshal pub: %v", err)
	}

	path := filepath.Join(t.TempDir(), "pub.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o600); err != nil {
		t.Fatalf("write pub: %v", err)
	}

	return path
}

func TestLoadSigningKey_PermsRefused(t *testing.T) {
	t.Parallel()

	k := genKey(t)

	// 0644 is group/world-readable → refused unless dev=true.
	loose := writeKeyPEM(t, k, false, 0o644)
	if _, err := LoadSigningKey(loose, nil, false); err == nil {
		t.Error("group/world-readable key accepted; want refusal")
	}

	if _, err := LoadSigningKey(loose, nil, true); err != nil {
		t.Errorf("dev=true should bypass the perm check: %v", err)
	}

	// 0600 → accepted.
	tight := writeKeyPEM(t, k, false, 0o600)
	if _, err := LoadSigningKey(tight, nil, false); err != nil {
		t.Errorf("0600 key rejected: %v", err)
	}
}

func TestLoadSigningKey_PKCS1AndPKCS8(t *testing.T) {
	t.Parallel()

	k := genKey(t)

	for _, pkcs8 := range []bool{false, true} {
		path := writeKeyPEM(t, k, pkcs8, 0o600)
		if _, err := LoadSigningKey(path, nil, false); err != nil {
			t.Errorf("pkcs8=%v load failed: %v", pkcs8, err)
		}
	}
}

func TestJWKS_ShapeAndRetiredKeys(t *testing.T) {
	t.Parallel()

	active := genKey(t)
	retired := genKey(t)

	sk, err := LoadSigningKey(writeKeyPEM(t, active, false, 0o600), []string{writePubPEM(t, &retired.PublicKey)}, false)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	raw := sk.JWKS()

	var doc struct {
		Keys []map[string]string `json:"keys"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal jwks: %v", err)
	}

	if len(doc.Keys) != 2 {
		t.Fatalf("jwks key count = %d, want 2 (active + retired)", len(doc.Keys))
	}

	first := doc.Keys[0]
	if first["kty"] != "RSA" || first["alg"] != "RS256" || first["use"] != "sig" {
		t.Errorf("unexpected jwk header: %v", first)
	}

	if first["kid"] != sk.KID() {
		t.Errorf("active kid = %q, want %q", first["kid"], sk.KID())
	}

	// n must decode back to the active modulus.
	nb, err := base64.RawURLEncoding.DecodeString(first["n"])
	if err != nil {
		t.Fatalf("decode n: %v", err)
	}

	if new(big.Int).SetBytes(nb).Cmp(active.N) != 0 {
		t.Error("jwks modulus does not match the active key")
	}
}

var signTestGrant = grant{
	ClientID: "client-1",
	Scope:    "analytics:read",
	Audience: "https://app.statnive.live/mcp",
	SiteIDs:  []uint32{1, 3},
}

const signTestIssuer = "https://app.statnive.live"

// signTestToken signs an access token for a fresh key and returns (token, key,
// signingKey).
func signTestToken(t *testing.T, now time.Time) (string, *rsa.PrivateKey, *SigningKey) {
	t.Helper()

	k := genKey(t)

	sk, err := LoadSigningKey(writeKeyPEM(t, k, false, 0o600), nil, false)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	g := signTestGrant
	g.UserID = uuid.New()

	tok, err := sk.SignAccessToken(g, signTestIssuer, now, 30*time.Minute)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	return tok, k, sk
}

// TestSignAccessToken_Signature proves an issued token is a 3-part RS256 JWT
// whose signature verifies against the public key — i.e. the in-tree
// resource-server verifier will accept it.
func TestSignAccessToken_Signature(t *testing.T) {
	t.Parallel()

	tok, k, sk := signTestToken(t, time.Unix(1_750_000_000, 0))

	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d parts, want 3", len(parts))
	}

	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode sig: %v", err)
	}

	if err := rsa.VerifyPKCS1v15(&k.PublicKey, crypto.SHA256, sum[:], sig); err != nil {
		t.Errorf("signature does not verify: %v", err)
	}

	var hdr struct {
		Alg, Kid string
	}

	hb, _ := base64.RawURLEncoding.DecodeString(parts[0])
	_ = json.Unmarshal(hb, &hdr)

	if hdr.Alg != "RS256" || hdr.Kid != sk.KID() {
		t.Errorf("header alg=%q kid=%q", hdr.Alg, hdr.Kid)
	}
}

// TestSignAccessToken_Claims proves the token carries the site_ids consent claim
// plus the registered claims the RS validates.
func TestSignAccessToken_Claims(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_750_000_000, 0)
	tok, _, _ := signTestToken(t, now)

	var claims accessClaims

	cb, _ := base64.RawURLEncoding.DecodeString(strings.Split(tok, ".")[1])
	_ = json.Unmarshal(cb, &claims)

	if claims.Aud != signTestGrant.Audience || claims.Scope != signTestGrant.Scope {
		t.Errorf("claims mismatch: %+v", claims)
	}

	if len(claims.SiteIDs) != 2 || claims.SiteIDs[0] != 1 || claims.SiteIDs[1] != 3 {
		t.Errorf("site_ids claim = %v, want [1 3]", claims.SiteIDs)
	}

	if claims.Exp != now.Add(30*time.Minute).Unix() {
		t.Errorf("exp = %d, want %d", claims.Exp, now.Add(30*time.Minute).Unix())
	}
}
