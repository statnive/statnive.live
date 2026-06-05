package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/statnive/statnive.live/internal/auth"
)

// fakeTokenStore is an in-memory APITokenStore for handler tests — no CH.
type fakeTokenStore struct {
	count      int
	createErr  error
	revokeErr  error
	list       []auth.MintedToken
	lastCreate struct {
		userID uuid.UUID
		name   string
		sites  []uint32
		role   auth.Role
		ttl    time.Duration
	}
	lastRevoke struct {
		tokenID uuid.UUID
		userID  uuid.UUID
	}
}

func (f *fakeTokenStore) Create(_ context.Context, userID uuid.UUID, name string, sites []uint32, role auth.Role, ttl time.Duration) (string, auth.MintedToken, error) {
	f.lastCreate.userID, f.lastCreate.name, f.lastCreate.sites, f.lastCreate.role, f.lastCreate.ttl = userID, name, sites, role, ttl
	if f.createErr != nil {
		return "", auth.MintedToken{}, f.createErr
	}

	return auth.APITokenPrefix + "rawsecret", auth.MintedToken{
		TokenID: uuid.New(), UserID: userID, Name: name, SiteIDs: sites, Role: role,
		CreatedAt: 1000,
	}, nil
}

func (f *fakeTokenStore) ListForUser(context.Context, uuid.UUID) ([]auth.MintedToken, error) {
	return f.list, nil
}

func (f *fakeTokenStore) Revoke(_ context.Context, tokenID, userID uuid.UUID) error {
	f.lastRevoke.tokenID, f.lastRevoke.userID = tokenID, userID

	return f.revokeErr
}

func (f *fakeTokenStore) LookupActive(context.Context, string) (auth.MintedToken, bool, error) {
	return auth.MintedToken{}, false, nil
}

func (f *fakeTokenStore) CountActiveForUser(context.Context, uuid.UUID) (int, error) {
	return f.count, nil
}

func tokenTestServer(t *testing.T, actor *auth.User, store auth.APITokenStore) http.Handler {
	t.Helper()

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(auth.WithSession(req.Context(), actor, nil)))
		})
	})

	MountMCPTokens(r, MCPTokenDeps{
		Tokens: store, MaxPerUser: 3, DefaultTTLDays: 90,
		PublicMCPURL: "https://app.statnive.live/mcp", HTTPEnabled: true,
	})

	return r
}

func sessionUser(sites map[uint32]auth.Role) *auth.User {
	return &auth.User{UserID: uuid.New(), Username: "alice", Role: auth.RoleAdmin, Sites: sites}
}

func do(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()

	var rdr *strings.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	} else {
		rdr = strings.NewReader("")
	}

	req := httptest.NewRequest(method, path, rdr)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

func TestMCPToken_Mint_ScopeClampRejectsEscalation(t *testing.T) {
	t.Parallel()

	store := &fakeTokenStore{}
	h := tokenTestServer(t, sessionUser(map[uint32]auth.Role{1: auth.RoleAdmin}), store)

	rec := do(t, h, http.MethodPost, "/api/mcp/tokens", `{"name":"x","site_ids":[2]}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("escalation to site 2 = %d, want 403", rec.Code)
	}

	if store.lastCreate.name != "" {
		t.Error("Create must not be called on a scope-clamp rejection")
	}
}

func TestMCPToken_Mint_DefaultsToCallerSites(t *testing.T) {
	t.Parallel()

	store := &fakeTokenStore{}
	h := tokenTestServer(t, sessionUser(map[uint32]auth.Role{3: auth.RoleViewer, 1: auth.RoleViewer}), store)

	rec := do(t, h, http.MethodPost, "/api/mcp/tokens", `{"name":"laptop"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("mint = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}

	// Default scope = all caller sites, sorted, role defaults to api.
	got := store.lastCreate
	if len(got.sites) != 2 || got.sites[0] != 1 || got.sites[1] != 3 {
		t.Errorf("default sites = %v, want [1 3]", got.sites)
	}

	if got.role != auth.RoleAPI {
		t.Errorf("default role = %q, want api", got.role)
	}
}

func TestMCPToken_Mint_RejectsTokenActor(t *testing.T) {
	t.Parallel()

	// An api-token / minted-token principal must not be able to mint more
	// tokens — a token can never beget another token.
	store := &fakeTokenStore{}
	tokenActor := &auth.User{UserID: uuid.New(), Username: "mcp-token:laptop", Role: auth.RoleAPI, Sites: map[uint32]auth.Role{1: auth.RoleAPI}}
	h := tokenTestServer(t, tokenActor, store)

	rec := do(t, h, http.MethodPost, "/api/mcp/tokens", `{"name":"x"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("token-actor mint = %d, want 403", rec.Code)
	}
}

func TestMCPToken_Mint_CapEnforced(t *testing.T) {
	t.Parallel()

	store := &fakeTokenStore{count: 3} // == MaxPerUser
	h := tokenTestServer(t, sessionUser(map[uint32]auth.Role{1: auth.RoleAdmin}), store)

	rec := do(t, h, http.MethodPost, "/api/mcp/tokens", `{"name":"x"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("over-cap mint = %d, want 409", rec.Code)
	}
}

func TestMCPToken_Mint_RawShownOnce(t *testing.T) {
	t.Parallel()

	store := &fakeTokenStore{}
	h := tokenTestServer(t, sessionUser(map[uint32]auth.Role{1: auth.RoleAdmin}), store)

	rec := do(t, h, http.MethodPost, "/api/mcp/tokens", `{"name":"x"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("mint = %d, want 201", rec.Code)
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if tok, _ := resp["token"].(string); !strings.HasPrefix(tok, auth.APITokenPrefix) {
		t.Errorf("response token = %q, want stnv_ prefixed raw", tok)
	}
}

func TestMCPToken_List_NeverLeaksRawOrHash(t *testing.T) {
	t.Parallel()

	store := &fakeTokenStore{list: []auth.MintedToken{{TokenID: uuid.New(), Name: "laptop", SiteIDs: []uint32{1}, Role: auth.RoleAPI}}}
	h := tokenTestServer(t, sessionUser(map[uint32]auth.Role{1: auth.RoleAdmin}), store)

	rec := do(t, h, http.MethodGet, "/api/mcp/tokens", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d, want 200", rec.Code)
	}

	body := rec.Body.String()
	if strings.Contains(body, "token_hash") || strings.Contains(strings.ToLower(body), `"token"`) {
		t.Errorf("list response leaks raw/hash: %s", body)
	}
}

func TestMCPToken_Revoke_CrossUserIsNotFound(t *testing.T) {
	t.Parallel()

	store := &fakeTokenStore{revokeErr: auth.ErrNotFound}
	h := tokenTestServer(t, sessionUser(map[uint32]auth.Role{1: auth.RoleAdmin}), store)

	rec := do(t, h, http.MethodDelete, "/api/mcp/tokens/"+uuid.New().String(), "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-user revoke = %d, want 404", rec.Code)
	}
}

func TestMCPToken_Connection_Shape(t *testing.T) {
	t.Parallel()

	store := &fakeTokenStore{}
	h := tokenTestServer(t, sessionUser(map[uint32]auth.Role{2: auth.RoleViewer, 1: auth.RoleViewer}), store)

	rec := do(t, h, http.MethodGet, "/api/mcp/connection", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("connection = %d, want 200", rec.Code)
	}

	var resp struct {
		URL     string   `json:"url"`
		Sites   []uint32 `json:"sites"`
		Command string   `json:"add_command_template"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if resp.URL != "https://app.statnive.live/mcp" {
		t.Errorf("url = %q", resp.URL)
	}

	if !strings.Contains(resp.Command, "claude mcp add --transport http https://app.statnive.live/mcp") {
		t.Errorf("add command malformed: %q", resp.Command)
	}
}
