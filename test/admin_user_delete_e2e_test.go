//go:build integration

// Integration + e2e for the admin hard-delete endpoint
// (DELETE /api/admin/users/{id}, internal/admin/users_handlers.go Delete).
//
// Proves, against a REAL ClickHouse + the REAL chi router (httptest), that a
// hard-delete cascades across every account-scoped store — sessions, per-site
// grants, self-serve MCP tokens, OAuth grants, and the users row — while a
// bystander on the same site is untouched, and that the self-delete guard
// holds. The last-admin (409) / not-found (404) / forbidden guards are pinned
// by the fast unit tests in internal/admin/users_delete_test.go.
//
// Skipped when CH is not reachable (make ch-up before running).
package integration_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/statnive/statnive.live/internal/admin"
	"github.com/statnive/statnive.live/internal/auth"
	"github.com/statnive/statnive.live/internal/privacy"
	"github.com/statnive/statnive.live/internal/storage"
)

func TestAdminUserHardDelete_E2E(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	store, err := storage.NewClickHouseStore(ctx, storage.Config{
		Addrs: []string{clickhouseAddr()}, Database: testDatabase, Username: "default",
	}, logger)
	if err != nil {
		t.Fatalf("clickhouse: %v", err)
	}
	defer func() { _ = store.Close() }()

	if mErr := storage.NewMigrationRunner(store.Conn(), storage.MigrationConfig{Database: testDatabase}, logger).Run(ctx); mErr != nil {
		t.Fatalf("migrate: %v", mErr)
	}

	conn := store.Conn()
	authStore := auth.NewClickHouseStore(conn, testDatabase)
	sitesStore := auth.NewClickHouseSitesStore(conn, testDatabase)
	eraser := privacy.NewEraseEnumerator(conn, testDatabase)

	// Dedicated site so hasOtherEnabledAdmin's ListUsers(site) is isolated from
	// other integration tests' users on site 1.
	const site = uint32(778899)
	h64 := func(seed string) string { return (seed + "0000000000000000000000000000000000000000000000000000000000000000")[:64] }

	uniq := uuid.New().String()[:8]
	actor := &auth.User{UserID: uuid.New(), SiteID: site, Email: "actor-" + uniq + "@x.z", Role: auth.RoleAdmin, Sites: map[uint32]auth.Role{site: auth.RoleAdmin}}
	target := &auth.User{UserID: uuid.New(), SiteID: site, Email: "target-" + uniq + "@x.z", Role: auth.RoleViewer}
	bystander := &auth.User{UserID: uuid.New(), SiteID: site, Email: "by-" + uniq + "@x.z", Role: auth.RoleViewer}

	for _, u := range []*auth.User{actor, target, bystander} {
		if cErr := authStore.CreateUser(ctx, u, "hash"); cErr != nil {
			t.Fatalf("create user %s: %v", u.Email, cErr)
		}
	}

	// Per-site grants for target + bystander.
	for _, u := range []*auth.User{target, bystander} {
		if gErr := sitesStore.Grant(ctx, u.UserID, site, auth.RoleViewer); gErr != nil {
			t.Fatalf("grant %s: %v", u.Email, gErr)
		}
	}

	// Active session for the target (proves the cascade revoke).
	var sh [32]byte
	copy(sh[:], target.UserID[:])
	if sErr := authStore.CreateSession(ctx, &auth.Session{
		IDHash: sh, UserID: target.UserID, SiteID: site, Role: auth.RoleViewer,
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}, [16]byte{}, "ua"); sErr != nil {
		t.Fatalf("create session: %v", sErr)
	}

	// Self-serve MCP token + OAuth grant for target AND bystander.
	insToken := func(u uuid.UUID, seed string) {
		if iErr := conn.Exec(ctx,
			"INSERT INTO "+testDatabase+".mcp_tokens (token_id, user_id, token_hash_hex, name, site_ids, role, version) VALUES (?, ?, ?, ?, ?, ?, ?)",
			uuid.New(), u, h64(seed), "tok", []uint32{site}, "viewer", uint64(time.Now().UnixNano()),
		); iErr != nil {
			t.Fatalf("insert mcp_token: %v", iErr)
		}
	}
	insOAuth := func(u uuid.UUID, seed string) {
		if iErr := conn.Exec(ctx,
			"INSERT INTO "+testDatabase+".oauth_refresh_tokens (token_hash, family_id, client_id, user_id, scope, audience, site_ids, expires_at, version) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
			h64(seed), uuid.New(), "c1", u, "analytics:read", "aud", []uint32{site}, time.Now().Add(time.Hour), uint64(time.Now().UnixNano()),
		); iErr != nil {
			t.Fatalf("insert oauth refresh: %v", iErr)
		}
	}
	insToken(target.UserID, "tgt-tok")
	insOAuth(target.UserID, "tgt-oauth")
	insToken(bystander.UserID, "by-tok")
	insOAuth(bystander.UserID, "by-oauth")

	deps := admin.Deps{Auth: authStore, UserSites: sitesStore, Eraser: eraser, Logger: logger}

	// Mount the REAL router behind a middleware that injects the admin actor —
	// exercises DELETE method routing → handler → real CH.
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			c := auth.WithSession(req.Context(), actor, &auth.Session{UserID: actor.UserID, SiteID: site, Role: auth.RoleAdmin})
			next.ServeHTTP(w, req.WithContext(c))
		})
	})
	admin.Mount(r, deps)
	srv := httptest.NewServer(r)
	defer srv.Close()

	doDelete := func(id uuid.UUID) int {
		req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/admin/users/"+id.String(), nil)
		resp, dErr := http.DefaultClient.Do(req)
		if dErr != nil {
			t.Fatalf("DELETE: %v", dErr)
		}
		_ = resp.Body.Close()

		return resp.StatusCode
	}

	countUserCol := func(table string, u uuid.UUID) uint64 {
		var n uint64
		if qErr := conn.QueryRow(ctx, "SELECT count() FROM "+testDatabase+"."+table+" FINAL WHERE user_id = ?", u).Scan(&n); qErr != nil {
			t.Fatalf("count %s: %v", table, qErr)
		}

		return n
	}

	// --- Self-delete guard (403) before the real delete. ---
	if code := doDelete(actor.UserID); code != http.StatusForbidden {
		t.Fatalf("self-delete = %d, want 403", code)
	}

	if _, gErr := authStore.GetUserByID(ctx, actor.UserID); gErr != nil {
		t.Fatal("actor removed by self-delete")
	}

	// --- Hard-delete the target. ---
	if code := doDelete(target.UserID); code != http.StatusNoContent {
		t.Fatalf("delete target = %d, want 204", code)
	}

	// users row gone (FINAL collapses; ALTER DELETE on users is synchronous-ish
	// but GetUserByID reads FINAL — poll briefly to be safe).
	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, gErr := authStore.GetUserByID(ctx, target.UserID); gErr != nil {
			break // gone
		}

		if time.Now().After(deadline) {
			t.Fatal("target users row not removed")
		}

		time.Sleep(250 * time.Millisecond)
	}

	// token + oauth erasure (async ALTER DELETE) — poll.
	for {
		if countUserCol("mcp_tokens", target.UserID) == 0 && countUserCol("oauth_refresh_tokens", target.UserID) == 0 {
			break
		}

		if time.Now().After(deadline) {
			t.Fatalf("target data not erased: tokens=%d oauth=%d",
				countUserCol("mcp_tokens", target.UserID), countUserCol("oauth_refresh_tokens", target.UserID))
		}

		time.Sleep(250 * time.Millisecond)
	}

	// per-site grant revoked.
	if grants, lErr := sitesStore.LoadUserSites(ctx, target.UserID); lErr != nil || len(grants) != 0 {
		t.Errorf("target grants not revoked: %v err=%v", grants, lErr)
	}

	// session revoked.
	if _, lErr := authStore.LookupSession(ctx, sh); lErr == nil {
		t.Error("target session still active after delete")
	}

	// --- Bystander untouched (cross-user isolation). ---
	if _, gErr := authStore.GetUserByID(ctx, bystander.UserID); gErr != nil {
		t.Error("bystander user removed")
	}

	if countUserCol("mcp_tokens", bystander.UserID) != 1 || countUserCol("oauth_refresh_tokens", bystander.UserID) != 1 {
		t.Errorf("bystander data erased (cross-user): tokens=%d oauth=%d",
			countUserCol("mcp_tokens", bystander.UserID), countUserCol("oauth_refresh_tokens", bystander.UserID))
	}

	if grants, lErr := sitesStore.LoadUserSites(ctx, bystander.UserID); lErr != nil || len(grants) != 1 {
		t.Errorf("bystander grant affected: %v err=%v", grants, lErr)
	}
}
