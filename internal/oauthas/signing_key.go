//go:build chatgpt_app

package oauthas

import (
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
)

// SigningKey holds the active RSA private key the AS signs access tokens with,
// plus any retired PUBLIC keys still served from /jwks.json during a rotation
// grace window (M7: default 24h). Signing always uses the active key; the
// resource-server verifier (cmd/statnive-live/mcp_oauth.go) selects the right
// key by `kid`, so publishing old + new lets in-flight tokens verify across a
// rotation.
//
// RS256 only — it matches the verifier's asymmetric-only, alg-confusion-guarded
// contract. The private key is loaded from an operator-provisioned PEM
// (chmod 0600, never in the repo/bundle; M8).
type SigningKey struct {
	priv    *rsa.PrivateKey
	kid     string
	retired []retiredKey // public-only keys served during rotation grace
	jwks    []byte       // precomputed RFC 7517 document (immutable after load)
}

type retiredKey struct {
	pub *rsa.PublicKey
	kid string
}

// LoadSigningKey reads the active RSA private key PEM (PKCS#1 or PKCS#8) and any
// retired public-key PEMs. It refuses a key file that is group- or
// world-readable (perm & 0o077) unless dev=true — a leaked signing key forges
// any token.
func LoadSigningKey(activePath string, retiredPubPaths []string, dev bool) (*SigningKey, error) {
	priv, err := readPrivateKeyPEM(activePath, dev)
	if err != nil {
		return nil, err
	}

	sk := &SigningKey{priv: priv, kid: thumbprint(&priv.PublicKey)}

	for _, p := range retiredPubPaths {
		pub, perr := readPublicKeyPEM(p)
		if perr != nil {
			return nil, fmt.Errorf("retired key %q: %w", p, perr)
		}

		sk.retired = append(sk.retired, retiredKey{pub: pub, kid: thumbprint(pub)})
	}

	// Precompute the JWKS document once — the key set is immutable after load,
	// so /jwks.json can serve cached bytes without re-marshalling per request.
	jwks, err := sk.marshalJWKS()
	if err != nil {
		return nil, fmt.Errorf("marshal jwks: %w", err)
	}

	sk.jwks = jwks

	return sk, nil
}

// KID returns the active key's RFC 7638 thumbprint (the JWT header `kid`).
func (k *SigningKey) KID() string { return k.kid }

func readPrivateKeyPEM(path string, dev bool) (*rsa.PrivateKey, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat signing key: %w", err)
	}

	if !dev && info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("signing key %q is group/world-readable (%o); chmod 0600", path, info.Mode().Perm())
	}

	raw, err := os.ReadFile(path) //nolint:gosec // operator-provisioned path, perms checked above
	if err != nil {
		return nil, fmt.Errorf("read signing key: %w", err)
	}

	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, errors.New("signing key: no PEM block")
	}

	if key, perr := x509.ParsePKCS1PrivateKey(block.Bytes); perr == nil {
		return key, nil
	}

	parsed, perr := x509.ParsePKCS8PrivateKey(block.Bytes)
	if perr != nil {
		return nil, fmt.Errorf("parse signing key (tried PKCS#1 and PKCS#8): %w", perr)
	}

	rsaKey, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("signing key is not RSA (RS256 required)")
	}

	return rsaKey, nil
}

func readPublicKeyPEM(path string) (*rsa.PublicKey, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // operator-provisioned path
	if err != nil {
		return nil, fmt.Errorf("read public key: %w", err)
	}

	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, errors.New("public key: no PEM block")
	}

	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse public key: %w", err)
	}

	pub, ok := parsed.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("public key is not RSA")
	}

	return pub, nil
}

// JWKS returns the precomputed RFC 7517 document (public active key + any
// retired keys) the resource-server verifier consumes. Immutable after load, so
// this is a cheap cached read.
func (k *SigningKey) JWKS() []byte { return k.jwks }

// marshalJWKS builds the RFC 7517 document. Shape matches mcp_oauth.go's `jwk`
// decoder exactly (kty/kid/n/e for RSA). Called once at load.
func (k *SigningKey) marshalJWKS() ([]byte, error) {
	keys := []map[string]string{rsaJWK(&k.priv.PublicKey, k.kid)}
	for _, r := range k.retired {
		keys = append(keys, rsaJWK(r.pub, r.kid))
	}

	return json.Marshal(map[string]any{"keys": keys})
}

func rsaJWK(pub *rsa.PublicKey, kid string) map[string]string {
	return map[string]string{
		"kty": "RSA",
		"use": "sig",
		"alg": "RS256",
		"kid": kid,
		"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}
}

// thumbprint is the RFC 7638 JWK thumbprint (SHA-256 over the canonical
// {"e","kty","n"} JSON), base64url — a stable, collision-resistant kid.
func thumbprint(pub *rsa.PublicKey) string {
	canonical := fmt.Sprintf(`{"e":%q,"kty":"RSA","n":%q}`,
		base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
		base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
	)
	sum := sha256.Sum256([]byte(canonical))

	return base64.RawURLEncoding.EncodeToString(sum[:])
}
