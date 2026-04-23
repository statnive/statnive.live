package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/statnive/statnive.live/internal/audit"
	"github.com/statnive/statnive.live/internal/httpjson"
)

// loginRequest is the tight-whitelist body for POST /api/login.
// F4 / Verification §52: DisallowUnknownFields() rejects any client
// that tries to sneak `role`, `site_id`, `is_admin`, or similar through
// the login form. The handler reads `site_id` from hostname mapping
// (or config default), never from the client.
type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// loginResponse is a thin "ok" — the session cookie does the lifting.
type loginResponse struct {
	User struct {
		UserID string `json:"user_id"`
		Email  string `json:"email"`
		Role   Role   `json:"role"`
		SiteID uint32 `json:"site_id"`
	} `json:"user"`
}

// HandlersConfig bundles per-deployment policy: default site_id for
// login, the master secret used for ip_hash on sessions rows (Privacy
// Rule 1), and the Phase 9 demo-login banner string.
//
// Session TTL + bcrypt cost live on MiddlewareDeps.CookieCfg.TTL and
// the password-hash sites respectively; this struct does not duplicate
// them.
type HandlersConfig struct {
	DefaultSiteID uint32
	MasterSecret  []byte
	DemoBanner    string
}

// Handlers bundles the three auth endpoints for chi wiring.
type Handlers struct {
	deps MiddlewareDeps
	cfg  HandlersConfig
	lock *Lockout
}

// NewHandlers constructs the handler set. lockout may be nil for
// deployments that rely on IP rate-limit only; production should always
// supply one. Cookie TTL comes from deps.CookieCfg.TTL.
func NewHandlers(deps MiddlewareDeps, cfg HandlersConfig, lock *Lockout) *Handlers {
	return &Handlers{deps: deps, cfg: cfg, lock: lock}
}

// Login handles POST /api/login. On success sets a session cookie and
// returns the safe user fields. On failure returns a uniform error body
// so the client can't distinguish unknown-user from wrong-password via
// response shape. Login rate-limit + per-email lockout are BOTH checked.
func (h *Handlers) Login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)

		return
	}

	req, ok := decodeLogin(w, r)
	if !ok {
		writeUnauthorized(w)

		return
	}

	emailHashAttr := slog.String("email_hash", hashForAudit(req.Email))

	u, authOK := h.authenticate(r, req, emailHashAttr)
	if !authOK {
		writeUnauthorized(w)

		return
	}

	h.mintSessionAndRespond(w, r, u, req.Email, emailHashAttr)
}

// decodeLogin reads + normalizes the login body. ok=false means the
// request body was malformed or missing required fields; caller writes
// the uniform 401.
//
// Uses admin.DecodeAllowed (Phase 3c) so the F4 mass-assignment guard
// is one helper with one implementation. The `{"role":"admin"}` attack
// body is rejected identically here and in every /api/admin/* handler.
func decodeLogin(_ http.ResponseWriter, r *http.Request) (loginRequest, bool) {
	var req loginRequest

	if err := httpjson.DecodeAllowed(r, &req, []string{"email", "password"}); err != nil {
		return loginRequest{}, false
	}

	req.Email = strings.ToLower(strings.TrimSpace(req.Email))

	if req.Email == "" || req.Password == "" {
		return loginRequest{}, false
	}

	return req, true
}

// authenticate runs the lockout check, user lookup, and password
// verification. Returns (*User, true) on success; (nil, false) on any
// failure with the reason already audited. The unknown-user branch
// runs bcrypt against dummyHash so its wall-time matches wrong-password.
func (h *Handlers) authenticate(
	r *http.Request, req loginRequest, emailHashAttr slog.Attr,
) (*User, bool) {
	if h.lock != nil {
		if lockErr := h.lock.Check(req.Email); lockErr != nil {
			h.emitLoginFailed(r.Context(), "locked_out", emailHashAttr, r)

			return nil, false
		}
	}

	u, pwHash, err := h.deps.Store.GetUserByEmail(r.Context(), h.cfg.DefaultSiteID, req.Email)
	if err != nil && !errors.Is(err, ErrNotFound) {
		if h.deps.Audit != nil {
			h.deps.Audit.Event(r.Context(), audit.EventLoginFailed,
				emailHashAttr,
				slog.String("reason", "store_error"),
				slog.String("err", err.Error()),
				slog.String("path", r.URL.Path),
			)
		}

		return nil, false
	}

	if errors.Is(err, ErrNotFound) || u == nil {
		_ = VerifyAgainstDummy(req.Password)

		if h.lock != nil {
			h.lock.Record(req.Email)
		}

		h.emitLoginFailed(r.Context(), "unknown_user", emailHashAttr, r)

		return nil, false
	}

	if u.Disabled {
		h.emitLoginFailed(r.Context(), "disabled", emailHashAttr, r)

		return nil, false
	}

	if verifyErr := VerifyPassword(pwHash, req.Password); verifyErr != nil {
		if h.lock != nil {
			h.lock.Record(req.Email)
		}

		h.emitLoginFailed(r.Context(), "bad_password", emailHashAttr, r)

		return nil, false
	}

	return u, true
}

// mintSessionAndRespond creates the session row, sets the cookie, and
// writes the JSON body. On infra error the 500 is already sent when
// this returns.
func (h *Handlers) mintSessionAndRespond(
	w http.ResponseWriter, r *http.Request, u *User, email string, emailHashAttr slog.Attr,
) {
	pair, err := NewToken()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	now := time.Now().UTC()
	sess := &Session{
		IDHash:     pair.Hash,
		UserID:     u.UserID,
		SiteID:     u.SiteID,
		Role:       u.Role,
		CreatedAt:  now.Unix(),
		LastUsedAt: now.Unix(),
		ExpiresAt:  now.Add(h.deps.CookieCfg.TTL).Unix(),
	}

	ipHash := h.ipHash(r)
	ua := truncateUA(r.UserAgent(), 512)

	if createErr := h.deps.Store.CreateSession(r.Context(), sess, ipHash, ua); createErr != nil {
		if h.deps.Audit != nil {
			h.deps.Audit.Event(r.Context(), audit.EventDashboardError,
				slog.String("path", r.URL.Path),
				slog.String("reason", "create_session"),
				slog.String("err", createErr.Error()),
			)
		}

		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	if h.lock != nil {
		h.lock.Clear(email)
	}

	http.SetCookie(w, CookieFromToken(h.deps.CookieCfg, pair.Raw, now))

	if h.deps.Audit != nil {
		h.deps.Audit.Event(r.Context(), audit.EventLoginSuccess,
			emailHashAttr,
			slog.String("role", string(u.Role)),
			slog.Uint64("site_id", uint64(u.SiteID)),
			slog.String("session_id_hash", hex.EncodeToString(pair.Hash[:])),
		)

		h.deps.Audit.Event(r.Context(), audit.EventSessionCreated,
			slog.String("session_id_hash", hex.EncodeToString(pair.Hash[:])),
			slog.Uint64("site_id", uint64(u.SiteID)),
		)
	}

	resp := loginResponse{}
	resp.User.UserID = u.UserID.String()
	resp.User.Email = u.Email
	resp.User.Role = u.Role
	resp.User.SiteID = u.SiteID

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// Logout handles POST /api/logout. Idempotent: clears the cookie and
// (if a cookie was present) revokes the session server-side. No auth
// required — a client with a stale cookie should still be able to
// clear it.
func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)

		return
	}

	cookie, err := r.Cookie(h.deps.CookieCfg.Name)
	if err == nil && cookie.Value != "" {
		hash := HashRawToken(cookie.Value)

		if revErr := h.deps.Store.RevokeSession(r.Context(), hash); revErr != nil &&
			!errors.Is(revErr, ErrNotFound) &&
			!errors.Is(revErr, ErrRevoked) &&
			!errors.Is(revErr, ErrExpired) {
			// Don't leak infrastructure errors to the client — still
			// clear the cookie so the user isn't stuck in limbo.
			if h.deps.Audit != nil {
				h.deps.Audit.Event(r.Context(), audit.EventDashboardError,
					slog.String("path", r.URL.Path),
					slog.String("reason", revErr.Error()),
				)
			}
		}

		if h.deps.Audit != nil {
			h.deps.Audit.Event(r.Context(), audit.EventLogout,
				slog.String("session_id_hash", hex.EncodeToString(hash[:])),
			)
		}
	}

	http.SetCookie(w, ClearCookie(h.deps.CookieCfg))
	w.WriteHeader(http.StatusNoContent)
}

// Me handles GET /api/user — returns the current authenticated user
// so the SPA can bootstrap. Requires a session; otherwise 401.
func (h *Handlers) Me(w http.ResponseWriter, r *http.Request) {
	u := UserFrom(r.Context())
	if u == nil {
		writeUnauthorized(w)

		return
	}

	resp := struct {
		UserID     string `json:"user_id"`
		Email      string `json:"email"`
		Username   string `json:"username"`
		Role       Role   `json:"role"`
		SiteID     uint32 `json:"site_id"`
		DemoBanner string `json:"demo_banner,omitempty"`
	}{
		UserID:     u.UserID.String(),
		Email:      u.Email,
		Username:   u.Username,
		Role:       u.Role,
		SiteID:     u.SiteID,
		DemoBanner: h.cfg.DemoBanner,
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// ipHash BLAKE3-128-hashes the client IP for the sessions row using
// the master secret. Zero when ClientIPFunc is not wired (unit tests).
func (h *Handlers) ipHash(r *http.Request) [16]byte {
	var out [16]byte

	if h.deps.ClientIPFunc == nil {
		return out
	}

	ip := h.deps.ClientIPFunc(r)
	if ip == "" {
		return out
	}

	// Reuse the identity package's plumbing once this method is wired;
	// for now hash IP+secret with SHA-256 and truncate to 16 bytes —
	// the audit row doesn't need BLAKE3-specific properties, just a
	// stable non-invertible fingerprint.
	sum := sha256.Sum256(append([]byte(ip), h.cfg.MasterSecret...))
	copy(out[:], sum[:16])

	return out
}

func (h *Handlers) emitLoginFailed(
	ctx context.Context, reason string, emailHashAttr slog.Attr, r *http.Request,
) {
	if h.deps.Audit == nil {
		return
	}

	h.deps.Audit.Event(ctx, audit.EventLoginFailed,
		emailHashAttr,
		slog.String("reason", reason),
		slog.String("path", r.URL.Path),
	)
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"invalid credentials"}`))
}

func hashForAudit(s string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(s))))

	return hex.EncodeToString(sum[:])
}

func truncateUA(ua string, limit int) string {
	if len(ua) <= limit {
		return ua
	}

	return ua[:limit]
}

// Compile-time sanity checks.
var (
	_ http.HandlerFunc = (&Handlers{}).Login
	_ http.HandlerFunc = (&Handlers{}).Logout
	_ http.HandlerFunc = (&Handlers{}).Me
)
