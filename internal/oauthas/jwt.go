//go:build chatgpt_app

package oauthas

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// accessClaims is the access-token payload. It mirrors the registered claims the
// resource-server verifier (cmd/statnive-live/mcp_oauth.go jwtClaims) checks —
// iss / aud / exp / nbf / scope — and adds the per-token `site_ids` claim (M1):
// the consented, scope-clamped sites this token may read. The RS builds its
// grant map from site_ids ∩ deployment AllowedSiteIDs, so consent is enforced
// per token rather than per deployment.
type accessClaims struct {
	Iss     string   `json:"iss"`
	Sub     string   `json:"sub"`
	Aud     string   `json:"aud"`
	Scope   string   `json:"scope"`
	SiteIDs []uint32 `json:"site_ids"`
	Iat     int64    `json:"iat"`
	Nbf     int64    `json:"nbf"`
	Exp     int64    `json:"exp"`
	Jti     string   `json:"jti"`
}

// SignAccessToken mints a signed RS256 JWT for the consented grant. Format is
// byte-compatible with the in-tree verifier: base64url(header).base64url(claims)
// .base64url(RS256 signature over SHA-256 of the signing input).
func (k *SigningKey) SignAccessToken(g grant, issuer string, now time.Time, ttl time.Duration) (string, error) {
	claims := accessClaims{
		Iss:     issuer,
		Sub:     g.UserID.String(),
		Aud:     g.Audience,
		Scope:   g.Scope,
		SiteIDs: g.SiteIDs,
		Iat:     now.Unix(),
		Nbf:     now.Unix(),
		Exp:     now.Add(ttl).Unix(),
		Jti:     uuid.NewString(),
	}

	return k.sign(claims)
}

func (k *SigningKey) sign(claims any) (string, error) {
	header := map[string]string{"alg": "RS256", "typ": "JWT", "kid": k.kid}

	hb, err := json.Marshal(header)
	if err != nil {
		return "", fmt.Errorf("marshal jwt header: %w", err)
	}

	cb, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal jwt claims: %w", err)
	}

	signingInput := base64.RawURLEncoding.EncodeToString(hb) + "." +
		base64.RawURLEncoding.EncodeToString(cb)

	sum := sha256.Sum256([]byte(signingInput))

	sig, err := rsa.SignPKCS1v15(rand.Reader, k.priv, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("sign jwt: %w", err)
	}

	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}
