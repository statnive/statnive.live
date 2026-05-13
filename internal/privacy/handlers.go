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
// necessary _statnive_optout cookie + a suppression-list entry keyed
// by the visitor's hashed cookieID. Subsequent ingest events from
// the same visitor are dropped at the ingest gate. Always returns
// 204 — opt-out is idempotent and the visitor MUST NOT learn whether
// they were already opted out.
func (h *Handlers) OptOut(w http.ResponseWriter, r *http.Request) {
	siteID, hash, ok := h.resolveSiteAndCookie(w, r)
	if !ok {
		return
	}

	// Add() is idempotent on duplicates because the in-memory set
	// is a hash-set, but every call still appends a new WAL line.
	// For Stage 2 we accept the WAL bloat — Stage 3 adds a "do
	// nothing if already suppressed" early-return when the volume
	// matters.
	if err := h.cfg.Suppression.Add(hash); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "_statnive_optout",
		Value:    "v1",
		Path:     "/",
		MaxAge:   int(365 * 24 * time.Hour / time.Second),
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})

	h.emit(r.Context(), audit.EventOptOutReceived, siteID, hash)

	w.WriteHeader(http.StatusNoContent)
}

// Access handles GET /api/privacy/access. Stage 2 returns a minimal
// JSON envelope acknowledging the visitor's request — the actual
// data export ships in v1.1 along with the email-token auth flow.
// Returns 401 when the visitor has no _statnive cookie (no anchor
// to identify their rows).
func (h *Handlers) Access(w http.ResponseWriter, r *http.Request) {
	siteID, hash, ok := h.resolveSiteAndCookie(w, r)
	if !ok {
		return
	}

	h.emit(r.Context(), audit.EventDSARAccessRequested, siteID, hash)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":           "received",
		"cookie_id_hash":   hash,
		"site_id":          siteID,
		"export_available": false,
		"export_message":   "Data export will be emailed within 30 days per GDPR Art. 15. Stage 2 only acknowledges the request.",
	})
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

	results, err := h.cfg.Erase.EraseByCookieID(r.Context(), hash)
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
//   - Look up site_id from the request Host
//   - Read the _statnive cookie (raw UUID)
//   - Hash to "h:" + hex per identity.HexCookieIDHash
//
// Writes the appropriate error response and returns ok=false on any
// failure so callers can early-return.
func (h *Handlers) resolveSiteAndCookie(w http.ResponseWriter, r *http.Request) (uint32, string, bool) {
	host := requestHost(r)
	if host == "" {
		http.Error(w, "host required", http.StatusBadRequest)

		return 0, "", false
	}

	siteID, _, err := h.cfg.Sites.LookupSitePolicy(r.Context(), host)
	if err != nil {
		http.Error(w, "unknown site", http.StatusNotFound)

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
