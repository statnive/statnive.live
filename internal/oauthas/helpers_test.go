//go:build chatgpt_app

package oauthas

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/statnive/statnive.live/internal/auth"
)

func challengeFor(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))

	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func TestPKCE_MethodS256Only(t *testing.T) {
	t.Parallel()

	for _, m := range []string{"S256"} {
		if !validChallengeMethod(m) {
			t.Errorf("method %q should be valid", m)
		}
	}

	for _, m := range []string{"", "plain", "PLAIN", "s256", "none", "RS256"} {
		if validChallengeMethod(m) {
			t.Errorf("method %q must be rejected (downgrade guard)", m)
		}
	}
}

func TestPKCE_VerifyS256(t *testing.T) {
	t.Parallel()

	verifier := strings.Repeat("a", 64) // valid 64-char verifier
	challenge := challengeFor(verifier)

	if !verifyPKCE(challenge, verifier) {
		t.Error("correct verifier rejected")
	}

	if verifyPKCE(challenge, strings.Repeat("b", 64)) {
		t.Error("wrong verifier accepted")
	}

	if verifyPKCE(challenge, "short") {
		t.Error("malformed (too short) verifier accepted")
	}

	if verifyPKCE("", verifier) {
		t.Error("empty challenge accepted")
	}
}

func TestPKCE_VerifierSyntax(t *testing.T) {
	t.Parallel()

	if validVerifier(strings.Repeat("a", 42)) {
		t.Error("42-char verifier accepted (min 43)")
	}

	if validVerifier(strings.Repeat("a", 129)) {
		t.Error("129-char verifier accepted (max 128)")
	}

	if validVerifier(strings.Repeat("a", 42) + " ") {
		t.Error("verifier with space accepted")
	}

	if !validVerifier(strings.Repeat("a", 43)) {
		t.Error("43-char verifier rejected")
	}
}

func TestChallengeSyntax(t *testing.T) {
	t.Parallel()

	good := challengeFor(strings.Repeat("a", 64)) // 43-char base64url
	if !validChallenge(good) {
		t.Errorf("valid challenge %q rejected", good)
	}

	if validChallenge("too-short") {
		t.Error("short challenge accepted")
	}

	if validChallenge(strings.Repeat("a", 43) + "=") {
		t.Error("challenge with padding accepted")
	}
}

func TestExactRedirectMatch(t *testing.T) {
	t.Parallel()

	reg := []string{"https://chatgpt.com/cb", "https://chatgpt.com/cb2"}

	if !exactRedirectMatch(reg, "https://chatgpt.com/cb") {
		t.Error("exact match rejected")
	}

	for _, bad := range []string{
		"https://chatgpt.com/cb/",         // trailing slash
		"https://chatgpt.com/cb?x=1",      // extra query
		"https://chatgpt.com/CB",          // case
		"https://chatgpt.com.evil.com/cb", // suffix smuggle
		"https://evil.com/cb",             // different host
		"http://chatgpt.com/cb",           // scheme downgrade
	} {
		if exactRedirectMatch(reg, bad) {
			t.Errorf("redirect smuggle accepted: %q", bad)
		}
	}
}

func TestResolveScope(t *testing.T) {
	t.Parallel()

	s := &Server{cfg: Config{Scope: "analytics:read"}}

	if got, ok := s.resolveScope(""); !ok || got != "analytics:read" {
		t.Errorf("empty scope: got %q ok=%v", got, ok)
	}

	if got, ok := s.resolveScope("analytics:read"); !ok || got != "analytics:read" {
		t.Errorf("exact scope: got %q ok=%v", got, ok)
	}

	if _, ok := s.resolveScope("analytics:read admin:write"); ok {
		t.Error("scope escalation (extra token) accepted")
	}

	if _, ok := s.resolveScope("admin:write"); ok {
		t.Error("wrong scope accepted")
	}
}

func TestConsentableSites(t *testing.T) {
	t.Parallel()

	s := &Server{cfg: Config{AllowedSiteIDs: []uint32{1, 2, 3}}}

	grants := map[uint32]auth.Role{1: auth.RoleViewer, 4: auth.RoleAdmin} // 4 outside ceiling
	got := s.consentableSites(grants)

	if len(got) != 1 || got[0] != 1 {
		t.Errorf("consentable = %v, want [1] (4 is outside the ceiling)", got)
	}
}

func TestValidateRedirectURIs(t *testing.T) {
	t.Parallel()

	good := [][]string{
		{"https://chatgpt.com/connector/callback"},
		{"http://localhost:8000/cb", "https://chatgpt.com/cb"},
	}
	for _, uris := range good {
		if err := validateRedirectURIs(uris); err != nil {
			t.Errorf("good uris %v rejected: %v", uris, err)
		}
	}

	bad := map[string][]string{
		"empty":       {},
		"http remote": {"http://chatgpt.com/cb"},
		"wildcard":    {"https://*.chatgpt.com/cb"},
		"fragment":    {"https://chatgpt.com/cb#x"},
		"relative":    {"/cb"},
		"too many":    {"https://a/1", "https://a/2", "https://a/3", "https://a/4", "https://a/5", "https://a/6", "https://a/7", "https://a/8", "https://a/9"},
	}
	for name, uris := range bad {
		if err := validateRedirectURIs(uris); err == nil {
			t.Errorf("%s: %v accepted, want rejection", name, uris)
		}
	}
}

func TestNewRawToken(t *testing.T) {
	t.Parallel()

	a, err := newRawToken()
	if err != nil {
		t.Fatalf("newRawToken: %v", err)
	}

	b, _ := newRawToken()

	if a == b {
		t.Error("two tokens collided")
	}

	// 32 bytes base64url (no padding) = 43 chars.
	if len(a) != 43 {
		t.Errorf("token len = %d, want 43", len(a))
	}
}
