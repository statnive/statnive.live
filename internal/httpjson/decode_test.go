package httpjson

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// loginLike mirrors the shape auth.Handlers uses for POST /api/login —
// exactly two fields, matches the Phase 2b attack pattern.
type loginLike struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func decodeWith(body string, dst any, allow []string) error {
	r := httptest.NewRequest(http.MethodPost, "/api/x",
		io.NopCloser(strings.NewReader(body)))

	return DecodeAllowed(r, dst, allow)
}

func TestDecodeAllowed_Happy(t *testing.T) {
	t.Parallel()

	var got loginLike

	err := decodeWith(
		`{"email":"a@b.c","password":"pw"}`, &got,
		[]string{"email", "password"},
	)
	if err != nil {
		t.Fatalf("DecodeAllowed: %v", err)
	}

	if got.Email != "a@b.c" || got.Password != "pw" {
		t.Errorf("got %+v", got)
	}
}

// TestDecodeAllowed_RejectsMassAssignmentAttack — canonical F4 body
// that tries to slip role + site_id + is_admin into a login request.
// Must return ErrMalformedBody; no partial write to dst.
func TestDecodeAllowed_RejectsMassAssignmentAttack(t *testing.T) {
	t.Parallel()

	var got loginLike

	err := decodeWith(
		`{"email":"a@b.c","password":"pw","role":"admin","site_id":99,"is_admin":true}`,
		&got,
		[]string{"email", "password"},
	)
	if !errors.Is(err, ErrMalformedBody) {
		t.Fatalf("got %v, want ErrMalformedBody", err)
	}
}

func TestDecodeAllowed_RejectsEmptyBody(t *testing.T) {
	t.Parallel()

	var got loginLike

	if err := decodeWith(``, &got, []string{"email", "password"}); !errors.Is(err, ErrMalformedBody) {
		t.Errorf("empty body: got %v", err)
	}
}

func TestDecodeAllowed_RejectsOversizedBody(t *testing.T) {
	t.Parallel()

	big := `{"email":"a@b.c","password":"` + strings.Repeat("x", MaxBodyBytes+1) + `"}`

	var got loginLike

	if err := decodeWith(big, &got, []string{"email", "password"}); !errors.Is(err, ErrMalformedBody) {
		t.Errorf("oversized: got %v", err)
	}
}

// TestDecodeAllowed_AllowListCatchesWidenedStruct — if a future
// refactor adds a Role field to loginLike but forgets to update
// allowedFields, the reflection assertion must fail BEFORE the
// attacker lands a role on the server.
func TestDecodeAllowed_AllowListCatchesWidenedStruct(t *testing.T) {
	t.Parallel()

	type widened struct {
		Email string `json:"email"`
		Role  string `json:"role"` // operator forgot to add this to allow-list
	}

	var got widened

	err := decodeWith(
		`{"email":"a@b.c","role":"admin"}`, &got,
		[]string{"email"},
	)
	if !errors.Is(err, ErrMalformedBody) {
		t.Errorf("widened struct without allow-list update: got %v", err)
	}
}

func TestDecodeAllowed_NilRequestRejected(t *testing.T) {
	t.Parallel()

	var got loginLike
	if err := DecodeAllowed(nil, &got, nil); !errors.Is(err, ErrMalformedBody) {
		t.Errorf("nil request: got %v", err)
	}
}
