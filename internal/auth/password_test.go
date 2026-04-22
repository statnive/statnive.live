package auth

import (
	"errors"
	"strings"
	"testing"
)

func TestHashPassword_RoundTrip(t *testing.T) {
	t.Parallel()

	h, err := HashPassword("correct horse battery staple", MinBcryptCost)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	if !strings.HasPrefix(h, "$2a$") && !strings.HasPrefix(h, "$2b$") {
		t.Errorf("unexpected hash prefix: %q", h[:4])
	}

	if vErr := VerifyPassword(h, "correct horse battery staple"); vErr != nil {
		t.Errorf("VerifyPassword(correct): %v", vErr)
	}

	if vErr := VerifyPassword(h, "wrong password"); !errors.Is(vErr, ErrBadCredentials) {
		t.Errorf("VerifyPassword(wrong): got %v, want ErrBadCredentials", vErr)
	}
}

func TestHashPassword_RejectsLowCost(t *testing.T) {
	t.Parallel()

	if _, err := HashPassword("pw", MinBcryptCost-1); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("cost < MinBcryptCost: got %v, want ErrInvalidInput", err)
	}
}

func TestHashPassword_RejectsEmpty(t *testing.T) {
	t.Parallel()

	if _, err := HashPassword("", MinBcryptCost); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("empty password: got %v, want ErrInvalidInput", err)
	}
}

func TestVerifyAgainstDummy_AlwaysBad(t *testing.T) {
	t.Parallel()

	for _, pw := range []string{"", "anything", "statnive-live-unknown-user-dummy-v1"} {
		if err := VerifyAgainstDummy(pw); !errors.Is(err, ErrBadCredentials) {
			t.Errorf("VerifyAgainstDummy(%q): got %v, want ErrBadCredentials", pw, err)
		}
	}
}
