package admin

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/statnive/statnive.live/internal/audit"
	"github.com/statnive/statnive.live/internal/auth"
	"github.com/statnive/statnive.live/internal/cache"
	"github.com/statnive/statnive.live/internal/storage"
)

// CNIL audience-measurement exemption (Sheet n°16) caps a consent-free
// deployment at three event types. A consent-free site with >3 distinct
// event_name values must either consolidate events, narrow the
// allow-list, or move out of consent-free mode.
const (
	eventAuditCapStatusOK   = "ok"
	eventAuditCapStatusOver = "over"
	eventAuditCNILCap       = 3

	eventAuditCacheTTL      = 5 * time.Minute
	eventAuditCacheCapacity = 256
	eventAuditDefaultWindow = 30 * 24 * time.Hour
)

type eventAuditResponse struct {
	SiteID     uint32                   `json:"site_id"`
	EventNames []storage.EventNameCount `json:"event_names"`
	Distinct   int                      `json:"distinct"`
	CapStatus  string                   `json:"cap_status"`
	WindowFrom time.Time                `json:"window_from"`
	WindowTo   time.Time                `json:"window_to"`
}

// EventAudit serves GET /api/admin/event-audit. One instance per
// process; per-site memoisation lives in the shared cache.Cache for
// LRU eviction + per-entry TTL.
type EventAudit struct {
	deps  Deps
	cache *cache.Cache
	now   func() time.Time
}

// NewEventAudit constructs the handler group.
func NewEventAudit(deps Deps) *EventAudit {
	return &EventAudit{
		deps:  deps,
		cache: cache.New(eventAuditCacheCapacity),
		now:   time.Now,
	}
}

// ServeHTTP requires admin role (the route is mounted under
// auth.RequireRole(auth.RoleAdmin) so the role check is handled
// upstream; we only verify the actor is present).
func (h *EventAudit) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	actor := auth.UserFrom(r.Context())
	if actor == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)

		return
	}

	if h.deps.EventAudit == nil {
		http.Error(w, "not configured", http.StatusServiceUnavailable)

		return
	}

	siteID, err := parseSiteIDQuery(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return
	}

	if actor.Sites != nil && !actor.CanAccessSite(siteID, auth.RoleViewer) {
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}

	now := h.now()
	to := now
	from := now.Add(-eventAuditDefaultWindow)

	value, loadErr := h.cache.Wrap(eventAuditCacheKey(siteID), eventAuditCacheTTL, func() (any, error) {
		rows, queryErr := h.deps.EventAudit.EventNameCardinality(r.Context(), siteID, from, to)
		if queryErr != nil {
			return nil, queryErr
		}

		return eventAuditResponse{
			SiteID:     siteID,
			EventNames: rows,
			Distinct:   len(rows),
			CapStatus:  capStatus(len(rows)),
			WindowFrom: from.UTC(),
			WindowTo:   to.UTC(),
		}, nil
	})
	if loadErr != nil {
		h.deps.emitDashboardError(r, "event_audit_query", loadErr)
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	body, ok := value.(eventAuditResponse)
	if !ok {
		h.deps.emitDashboardError(r, "event_audit_query", errors.New("cache returned unexpected type"))
		http.Error(w, "internal error", http.StatusInternalServerError)

		return
	}

	if h.deps.Audit != nil {
		h.deps.Audit.Event(r.Context(), audit.EventDashboardOK,
			slog.String("path", r.URL.Path),
			slog.Uint64("site_id", uint64(siteID)),
			slog.Int("distinct", body.Distinct),
			slog.String("cap_status", body.CapStatus),
		)
	}

	writeJSON(w, http.StatusOK, body)
}

func eventAuditCacheKey(siteID uint32) string {
	return "event-audit:" + strconv.FormatUint(uint64(siteID), 10)
}

func capStatus(distinct int) string {
	if distinct > eventAuditCNILCap {
		return eventAuditCapStatusOver
	}

	return eventAuditCapStatusOK
}

var (
	errMissingSiteID = errors.New("site_id query param required")
	errInvalidSiteID = errors.New("site_id must be a positive integer")
)

func parseSiteIDQuery(r *http.Request) (uint32, error) {
	raw := r.URL.Query().Get("site_id")
	if raw == "" {
		return 0, errMissingSiteID
	}

	n, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || n == 0 {
		return 0, errInvalidSiteID
	}

	return uint32(n), nil
}
