package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"testing"
	"time"
)

func TestNewToken_EntropyAndShape(t *testing.T) {
	t.Parallel()

	seen := make(map[string]struct{}, 100)

	for i := range 100 {
		p, err := NewToken()
		if err != nil {
			t.Fatalf("NewToken[%d]: %v", i, err)
		}

		if len(p.Raw) != tokenRawBytes*2 {
			t.Fatalf("NewToken[%d].Raw length = %d, want %d", i, len(p.Raw), tokenRawBytes*2)
		}

		if _, err := hex.DecodeString(p.Raw); err != nil {
			t.Fatalf("NewToken[%d].Raw not hex: %v", i, err)
		}

		want := sha256.Sum256([]byte(p.Raw))
		if p.Hash != want {
			t.Fatalf("NewToken[%d].Hash != sha256(Raw)", i)
		}

		if _, dup := seen[p.Raw]; dup {
			t.Fatalf("NewToken[%d] collision at %q — entropy source broken", i, p.Raw)
		}

		seen[p.Raw] = struct{}{}
	}
}

func TestHashRawToken_ConstantHash(t *testing.T) {
	t.Parallel()

	raw := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	want := sha256.Sum256([]byte(raw))

	if got := HashRawToken(raw); got != want {
		t.Errorf("HashRawToken mismatch")
	}
}

func TestCookieFromToken_Attributes(t *testing.T) {
	t.Parallel()

	cfg := SessionCookieConfig{
		Name:     "statnive_session",
		TTL:      14 * 24 * time.Hour,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	}
	now := time.Unix(1_800_000_000, 0).UTC()

	c := CookieFromToken(cfg, "raw-value", now)
	if c.Name != cfg.Name {
		t.Errorf("Name = %q, want %q", c.Name, cfg.Name)
	}

	if !c.HttpOnly {
		t.Error("HttpOnly must be true")
	}

	if !c.Secure {
		t.Error("Secure must be true when cfg.Secure=true")
	}

	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", c.SameSite)
	}

	if c.MaxAge != int(cfg.TTL.Seconds()) {
		t.Errorf("MaxAge = %d, want %d", c.MaxAge, int(cfg.TTL.Seconds()))
	}

	if c.Path != "/" {
		t.Errorf("Path = %q, want /", c.Path)
	}
}

func TestClearCookie_EvictsImmediately(t *testing.T) {
	t.Parallel()

	cfg := SessionCookieConfig{Name: "statnive_session", Secure: true, SameSite: http.SameSiteLaxMode}

	c := ClearCookie(cfg)
	if c.Value != "" {
		t.Errorf("Value = %q, want empty", c.Value)
	}

	if c.MaxAge != -1 {
		t.Errorf("MaxAge = %d, want -1", c.MaxAge)
	}
}
