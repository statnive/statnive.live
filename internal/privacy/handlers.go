package privacy

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/statnive/statnive.live/internal/audit"
	"github.com/statnive/statnive.live/internal/identity"
	"github.com/statnive/statnive.live/internal/middleware"
	"github.com/statnive/statnive.live/internal/sites"
)

// Config bundles the dependencies the three handlers share. Every
// field is required in production.
type Config struct {
	// Sites resolves hostnames to site_id + policy. Mirrors the
	// ingest handler's SiteResolver — the handler only reaches into
	// LookupSiteIDByHostname so the contract stays narrow.
	Sites SitesResolver

	// MasterSecret hashes the raw _statnive UUID into the at-rest
	// "h:" + hex form so cookie-driven lookups can join
	// events_raw.cookie_id.
	MasterSecret []byte

	// Suppression is the in-memory + WAL-backed opt-out store.
	Suppression *SuppressionList

	// Erase deletes the visitor's rows across every base table that
	// carries cookie_id. Optional: when nil the erase endpoint
	// returns 503 (Stage 2 only-suppression mode).
	Erase *EraseEnumerator

	// Export returns the visitor's events_raw rows for Art. 15 / Art. 20
	// fulfilment. Optional: when nil the access endpoint returns 503.
	// Production deployments MUST wire this alongside Erase so the
	// surface a visitor can erase equals the surface they can read.
	Export *VisitorExporter

	// Audit emits the privacy.* events. Optional — nil-safe for tests.
	Audit *audit.Logger

	// Now is the wall-clock source — injectable for tests; defaults
	// to time.Now if zero.
	Now func() time.Time
}

// SitesResolver is the minimum interface the privacy handlers need.
// Production: *sites.Registry; tests: a fake.
type SitesResolver interface {
	LookupSitePolicy(ctx context.Context, hostname string) (uint32, sites.SitePolicy, error)
}

// Handlers exposes the three privacy endpoints. Construct once per
// process and mount via NewHandlers + Mount-style wiring in main.go.
type Handlers struct {
	cfg Config
}

// NewHandlers validates the config and returns a ready handler set.
func NewHandlers(cfg Config) (*Handlers, error) {
	if cfg.Sites == nil {
		return nil, errors.New("privacy: Sites is required")
	}

	if len(cfg.MasterSecret) == 0 {
		return nil, errors.New("privacy: MasterSecret is required")
	}

	if cfg.Suppression == nil {
		return nil, errors.New("privacy: Suppression is required")
	}

	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	return &Handlers{cfg: cfg}, nil
}

// OptOut handles POST /api/privacy/opt-out. Writes the strictly-
// necessary _statnive_optout_<site_id> cookie unconditionally so
// even a fresh visitor (no prior _statnive) can opt out — the
// per-site cookie itself is the suppression signal at the ingest
// gate. When a _statnive cookie IS present its hash is also added
// to the suppression list as a belt-and-braces anchor that survives
// CHIPS partition resets. Always returns 204 — opt-out is
// idempotent and the visitor MUST NOT learn whether they were
// already opted out.
func (h *Handlers) OptOut(w http.ResponseWriter, r *http.Request) {
	siteID, status := h.resolveSiteID(r)
	if status != 0 {
		http.Error(w, http.StatusText(status), status)

		return
	}

	// Hash + suppression-list anchor are optional: they only apply
	// when the visitor already carries a _statnive identifier. The
	// cookie-based ingest gate (handler.go:hasOptOutCookie) covers
	// the no-identifier case.
	var hash string
	if c, err := r.Cookie("_statnive"); err == nil && c.Value != "" {
		hash = identity.HexCookieIDHash(h.cfg.MasterSecret, siteID, c.Value)
		if addErr := h.cfg.Suppression.Add(hash); addErr != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)

			return
		}
	}

	http.SetCookie(w, &http.Cookie{
		Name:        optoutCookieName(siteID),
		Value:       "v1",
		Path:        "/",
		MaxAge:      int(365 * 24 * time.Hour / time.Second),
		HttpOnly:    true,
		Secure:      isHTTPS(r),
		SameSite:    http.SameSiteNoneMode,
		Partitioned: true,
	})

	h.emit(r.Context(), audit.EventOptOutReceived, siteID, hash)

	w.WriteHeader(http.StatusNoContent)
}

// Access handles GET /api/privacy/access. Returns the visitor's
// events_raw rows for the requesting site_id as JSON — satisfies both
// Art. 15 Auskunft and Art. 20 Datenübertragbarkeit in one
// round-trip. Returns 401 when the visitor has no _statnive cookie
// (no anchor to identify their rows) and 503 when no VisitorExporter
// is wired (test stacks that opt out of ClickHouse).
func (h *Handlers) Access(w http.ResponseWriter, r *http.Request) {
	siteID, hash, ok := h.resolveSiteAndCookie(w, r)
	if !ok {
		return
	}

	h.emit(r.Context(), audit.EventDSARAccessRequested, siteID, hash)

	if h.cfg.Export == nil {
		http.Error(w, "access not configured", http.StatusServiceUnavailable)

		return
	}

	result, err := h.cfg.Export.ExportVisitorRows(r.Context(), siteID, hash)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	h.emit(r.Context(), audit.EventDSARAccessReturned, siteID, hash,
		slog.Int("row_count", result.RowCount),
		slog.Bool("truncated", result.Truncated),
	)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, no-store")
	_ = json.NewEncoder(w).Encode(result)
}

// Erase handles POST /api/privacy/erase. Issues an async ALTER TABLE
// ... DELETE across every base MergeTree table that carries cookie_id;
// returns 202 (mutations run in the background). Failure of any
// per-table mutation is surfaced in the response so the operator can
// re-issue.
func (h *Handlers) Erase(w http.ResponseWriter, r *http.Request) {
	siteID, hash, ok := h.resolveSiteAndCookie(w, r)
	if !ok {
		return
	}

	if h.cfg.Erase == nil {
		http.Error(w, "erase not configured", http.StatusServiceUnavailable)

		return
	}

	results, err := h.cfg.Erase.EraseByCookieID(r.Context(), siteID, hash)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	// An erase implicitly opts the visitor out — the rows are going
	// away, but new events from the same cookie should also stop
	// landing. Match what /api/privacy/opt-out does.
	if addErr := h.cfg.Suppression.Add(hash); addErr != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	h.emit(r.Context(), audit.EventDSAREraseRequested, siteID, hash,
		slog.Int("tables", len(results)),
	)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":  "accepted",
		"tables":  results,
		"site_id": siteID,
	})
}

// resolveSiteAndCookie does the shared prelude:
//   - Resolve site_id, in priority order:
//     1. ctxKeySiteFromOrigin stashed by CORS middleware (cross-origin).
//     2. X-Statnive-Site header (same-origin /privacy fallback —
//     validated against the registry).
//     3. r.Host (legacy same-origin path).
//   - Read the _statnive cookie (raw UUID).
//   - Hash to "h:" + hex per identity.HexCookieIDHash.
//
// Writes the appropriate error response and returns ok=false on any
// failure so callers can early-return.
func (h *Handlers) resolveSiteAndCookie(w http.ResponseWriter, r *http.Request) (uint32, string, bool) {
	siteID, status := h.resolveSiteID(r)
	if status != 0 {
		http.Error(w, http.StatusText(status), status)

		return 0, "", false
	}

	cookie, err := r.Cookie("_statnive")
	if err != nil || cookie.Value == "" {
		http.Error(w, "no statnive cookie", http.StatusUnauthorized)

		return 0, "", false
	}

	hash := identity.HexCookieIDHash(h.cfg.MasterSecret, siteID, cookie.Value)

	return siteID, hash, true
}

// resolveSiteID applies the Stage-4 priority chain. Returns site_id
// + the HTTP status to write on failure: 0 = success, 400 = no host
// signal at all, 404 = host signal present but unrecognised.
//
// Priority:
//  1. ctxKeySiteFromOrigin stashed by CORS middleware (cross-origin).
//  2. X-Statnive-Site header (same-origin /privacy fallback).
//  3. r.Host (legacy same-origin path).
func (h *Handlers) resolveSiteID(r *http.Request) (uint32, int) {
	if id, ok := middleware.SiteIDFromOriginContext(r.Context()); ok && id != 0 {
		return id, 0
	}

	if hint := r.Header.Get("X-Statnive-Site"); hint != "" {
		id, _, err := h.cfg.Sites.LookupSitePolicy(r.Context(), hint)
		if err != nil || id == 0 {
			return 0, http.StatusNotFound
		}

		return id, 0
	}

	host := requestHost(r)
	if host == "" {
		return 0, http.StatusBadRequest
	}

	id, _, err := h.cfg.Sites.LookupSitePolicy(r.Context(), host)
	if err != nil || id == 0 {
		return 0, http.StatusNotFound
	}

	return id, 0
}

func (h *Handlers) emit(ctx context.Context, name audit.EventName, siteID uint32, hash string, extra ...slog.Attr) {
	if h.cfg.Audit == nil {
		return
	}

	attrs := append([]slog.Attr{
		slog.Uint64("site_id", uint64(siteID)),
		slog.String("cookie_id_hash", hash),
	}, extra...)

	h.cfg.Audit.Event(ctx, name, attrs...)
}

// requestHost strips any :port suffix off r.Host. chi sets r.Host to
// the value of the Host header; Strip trailing port so a hostname
// lookup against "example.com:8080" still resolves.
func requestHost(r *http.Request) string {
	host := r.Host
	if host == "" {
		host = r.URL.Host
	}

	for i := range len(host) {
		if host[i] == ':' {
			return host[:i]
		}
	}

	return host
}

func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}

	return r.Header.Get("X-Forwarded-Proto") == "https"
}
