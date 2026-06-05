package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestHashTokenHex_MatchesSHA256(t *testing.T) {
	t.Parallel()

	raw := APITokenPrefix + "deadbeef"
	want := sha256.Sum256([]byte(raw))

	if got := HashTokenHex(raw); got != hex.EncodeToString(want[:]) {
		t.Errorf("HashTokenHex mismatch: %s", got)
	}
}

func TestGenerateRawToken_PrefixedAndUnique(t *testing.T) {
	t.Parallel()

	seen := make(map[string]struct{}, 100)

	for range 100 {
		raw, err := generateRawToken()
		if err != nil {
			t.Fatalf("generateRawToken: %v", err)
		}

		if !strings.HasPrefix(raw, APITokenPrefix) {
			t.Fatalf("token missing prefix: %q", raw)
		}

		if _, dup := seen[raw]; dup {
			t.Fatalf("CSPRNG collision: %q", raw)
		}

		seen[raw] = struct{}{}
	}
}

// dynStoreStub implements APITokenStore; only LookupActive is exercised.
type dynStoreStub struct{ byHash map[string]MintedToken }

func (dynStoreStub) Create(context.Context, uuid.UUID, string, []uint32, Role, time.Duration) (string, MintedToken, error) {
	return "", MintedToken{}, nil
}
func (dynStoreStub) ListForUser(context.Context, uuid.UUID) ([]MintedToken, error) { return nil, nil }
func (dynStoreStub) Revoke(context.Context, uuid.UUID, uuid.UUID) error            { return nil }
func (dynStoreStub) CountActiveForUser(context.Context, uuid.UUID) (int, error)    { return 0, nil }
func (s dynStoreStub) LookupActive(_ context.Context, hashHex string) (MintedToken, bool, error) {
	m, ok := s.byHash[hashHex]

	return m, ok, nil
}

// TestAPITokenMiddleware_DynamicTokenIsSiteScoped proves a dashboard-minted
// token authenticates AND is scoped to exactly its site_ids — never the
// uuid.Nil+SiteID==0 wildcard branch.
func TestAPITokenMiddleware_DynamicTokenIsSiteScoped(t *testing.T) {
	t.Parallel()

	raw := APITokenPrefix + "abc123"
	hashHex := HashTokenHex(raw)
	minter := uuid.New()

	deps := MiddlewareDeps{
		DynamicTokens: dynStoreStub{byHash: map[string]MintedToken{
			hashHex: {TokenID: uuid.New(), UserID: minter, Role: RoleAPI, SiteIDs: []uint32{1, 4}},
		}},
	}

	var captured *User

	h := APITokenMiddleware(deps)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		captured = UserFrom(r.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/stats/overview?site=1", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if captured == nil {
		t.Fatal("dynamic token did not authenticate")
	}

	if captured.UserID != minter {
		t.Errorf("UserID = %v, want minter %v (non-nil ⇒ grant-map branch)", captured.UserID, minter)
	}

	if !captured.ActorCanReadSite(1) || !captured.ActorCanReadSite(4) {
		t.Error("token must read its scoped sites 1 and 4")
	}

	if captured.ActorCanReadSite(99) {
		t.Error("token must NOT read an out-of-scope site (no wildcard)")
	}
}

// TestAPITokenMiddleware_UnknownDynamicToken passes through unauthenticated.
func TestAPITokenMiddleware_UnknownDynamicToken(t *testing.T) {
	t.Parallel()

	deps := MiddlewareDeps{DynamicTokens: dynStoreStub{byHash: map[string]MintedToken{}}}

	var captured *User

	h := APITokenMiddleware(deps)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		captured = UserFrom(r.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+APITokenPrefix+"nope")
	h.ServeHTTP(httptest.NewRecorder(), req)

	if captured != nil {
		t.Error("unknown token must not attach a user")
	}
}
