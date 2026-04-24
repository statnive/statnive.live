package ingest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/statnive/statnive.live/internal/audit"
	"github.com/statnive/statnive.live/internal/identity"
	"github.com/statnive/statnive.live/internal/sites"
)

// Enricher is the contract handler.serve calls for the synchronous
// 6-stage enrichment. The real implementation (enrich.Pipeline) runs
// inline on the handler goroutine — no worker pool, no in-memory
// channel — so the handler can wait for the WAL fsync immediately
// after enrichment.
type Enricher interface {
	Enrich(raw *RawEvent) (EnrichedEvent, bool)
}

// WALSyncer is the contract handler.serve calls to durably persist the
// enriched event before responding 202. AppendAndWait blocks until the
// containing batch has been fsynced (group commit). Sync errors return
// the underlying error AND terminate the process via the GroupSyncer's
// fatal path; the handler returns 503 if the ack ever surfaces an error.
type WALSyncer interface {
	AppendAndWait(ctx context.Context, ev EnrichedEvent) (uint64, error)
}

const (
	maxBodyBytes  = 8 * 1024 // PLAN.md:153 — Security feature #4 (8 KB MaxBytesReader)
	maxArrayItems = 10       // single tracker request batches at most 10 events
	uaMinLen      = 16       // PLAN.md:156 + doc 24 §Sec 1.6 fast-reject gate
	uaMaxLen      = 500
)

// SiteResolver is the subset of sites.Registry that the handler needs.
// Defined here so the handler test can inject a fake.
type SiteResolver interface {
	LookupSiteIDByHostname(ctx context.Context, hostname string) (uint32, error)
}

// HandlerConfig groups the dependencies + tunables.
type HandlerConfig struct {
	// Pipeline runs the 6-stage enrichment inline. Required.
	Pipeline Enricher
	// WAL persists each enriched event before the handler responds 202.
	// Required — the doc 27 §Gap 1 ack-after-fsync contract is what the
	// handler is gating on.
	WAL   WALSyncer
	Sites SiteResolver
	// MasterSecret is the same key material identity.NewSaltManager
	// receives. The handler uses it to hash any raw user_id sent by the
	// tracker into a per-tenant SHA-256 (identity.HexUserIDHash) before
	// the event enters the pipeline. Empty MasterSecret skips hashing,
	// which the test fakes rely on (handler_test.go injects nil here).
	MasterSecret []byte
	Audit        *audit.Logger    // optional — nil silences audit emissions (test mode)
	Now          func() time.Time // injectable for tests; defaults to time.Now
	Logger       *slog.Logger
}

// NewHandler returns the http.Handler wired for POST /api/event.
func NewHandler(cfg HandlerConfig) http.Handler {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	hashUserID := len(cfg.MasterSecret) > 0

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serve(w, r, cfg, hashUserID)
	})
}

func serve(w http.ResponseWriter, r *http.Request, cfg HandlerConfig, hashUserID bool) {
	// FastRejectMiddleware enforces POST-only + the prefetch/UA-shape
	// fast-reject before any downstream middleware. By the time we get
	// here the request has passed both checks and the rate limiter.
	ua := r.Header.Get("User-Agent")

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	defer func() { _ = r.Body.Close() }()

	events, err := parseBody(r.Body)
	if err != nil {
		cfg.Logger.Debug("body parse failed", "err", err)
		http.Error(w, "bad request", http.StatusBadRequest)

		return
	}

	if len(events) == 0 {
		w.WriteHeader(http.StatusNoContent)

		return
	}

	now := cfg.Now().UTC()
	ip := ClientIP(r)
	cookieID := readOrSetCookieID(w, r)

	for i := range events {
		raw := &events[i]

		// Server-authoritative — TSUTC, UserAgent, IP, CookieID are
		// json:"-" on RawEvent. Trust the request, not the body.
		raw.TSUTC = now
		raw.UserAgent = ua
		raw.IP = ip
		raw.CookieID = cookieID

		siteID, sErr := cfg.Sites.LookupSiteIDByHostname(r.Context(), strings.ToLower(raw.Hostname))
		if sErr != nil {
			cfg.Logger.Debug("unknown hostname", "hostname", raw.Hostname)
			emitAudit(r.Context(), cfg.Audit, audit.EventHostnameUnknown,
				slog.String("hostname", raw.Hostname),
			)
			// Drop unknown-hostname events silently with 204 — doc 24 calls out
			// this is what trackers expect. Bot scrapers will see no signal.
			w.WriteHeader(http.StatusNoContent)

			return
		}

		raw.SiteID = siteID

		// Hash user_id with the per-tenant key material, then wipe the
		// raw value before it can reach the pipeline / WAL / batch
		// writer. Privacy Rule 4: only SHA-256(master_secret || site_id
		// || user_id) ever lands in events_raw.user_id_hash. If
		// hashUserID is false (no master_secret configured), the raw
		// value is still cleared — silently dropping the uid is the
		// stricter privacy stance.
		if hashUserID && raw.UserID != "" {
			raw.UserIDHash = identity.HexUserIDHash(cfg.MasterSecret, raw.SiteID, raw.UserID)
		}

		raw.UserID = ""

		// Synchronous 6-stage enrichment runs on the handler goroutine.
		// Burst-dropped events skip the WAL silently — they're a known
		// rejection class, not a server failure.
		enriched, ok := cfg.Pipeline.Enrich(raw)
		if !ok {
			continue
		}

		// Block until the WAL has fsynced this event. The 202 below is
		// the ack-after-fsync contract: client knows we have it durably.
		// AppendAndWait surfaces ctx cancel + Sync errors + group-syncer
		// shutdown — all map to 503 (client retries; on Sync error the
		// process is also exiting).
		if _, walErr := cfg.WAL.AppendAndWait(r.Context(), enriched); walErr != nil {
			cfg.Logger.Warn("wal append-and-wait failed",
				"err", walErr, "site_id", raw.SiteID)
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)

			return
		}
	}

	w.WriteHeader(http.StatusAccepted)
}

// fastReject returns a non-empty reason string when the request should be
// dropped before any pipeline work. Order is cheap-first per doc 24 §Sec 1.3.
func fastReject(h http.Header, ua string) string {
	switch {
	case h.Get("X-Purpose") == "prefetch",
		h.Get("Purpose") == "prefetch",
		h.Get("X-Moz") == "prefetch":
		return "prefetch-header"
	}

	uaLen := len(ua)
	if uaLen < uaMinLen || uaLen > uaMaxLen {
		return "ua-length"
	}

	if !isASCII(ua) {
		return "ua-non-ascii"
	}

	if isIPLike(ua) {
		return "ua-as-ip"
	}

	if isUUIDLike(ua) {
		return "ua-as-uuid"
	}

	return ""
}

func isASCII(s string) bool {
	for i := range len(s) {
		if s[i] > 127 {
			return false
		}
	}

	return true
}

func isIPLike(s string) bool {
	return net.ParseIP(strings.TrimSpace(s)) != nil
}

func isUUIDLike(s string) bool {
	_, err := uuid.Parse(strings.TrimSpace(s))

	return err == nil
}

// parseBody accepts either a single JSON object or an array (max 10 items).
// We buffer the body to a small in-memory slice so we can peek the first
// non-whitespace byte without consuming any tokens — that's cheaper than
// the previous "tokenize + reassemble" workaround and the body is already
// capped at 8 KiB by MaxBytesReader.
func parseBody(r io.Reader) ([]RawEvent, error) {
	body, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}

	trimmed := bytes.TrimLeft(body, " \t\r\n")
	if len(trimmed) == 0 {
		return nil, errors.New("empty body")
	}

	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()

	switch trimmed[0] {
	case '[':
		var arr []RawEvent
		if decErr := dec.Decode(&arr); decErr != nil {
			return nil, decErr
		}

		if len(arr) > maxArrayItems {
			return nil, errors.New("too many events in batch")
		}

		return arr, nil
	case '{':
		var ev RawEvent
		if decErr := dec.Decode(&ev); decErr != nil {
			return nil, decErr
		}

		return []RawEvent{ev}, nil
	default:
		return nil, errors.New("expected '[' or '{'")
	}
}

// ClientIP honors proxy headers in priority order. The result is used for
// GeoIP enrichment and rate-limit keying; it is never persisted (Privacy
// Rule 1 is enforced by the EnrichedEvent struct having no IP field).
//
// Header priority: True-Client-IP → CF-Connecting-IP → X-Real-IP → rightmost
// X-Forwarded-For. Rightmost XFF is the last trusted proxy hop.
//
// Exported so internal/ratelimit can key by the same value the handler
// uses for audit-log emissions — without sharing this helper, "who sent
// the request" would diverge between the two layers.
func ClientIP(r *http.Request) string {
	for _, key := range []string{"True-Client-IP", "CF-Connecting-IP", "X-Real-IP"} {
		if v := strings.TrimSpace(r.Header.Get(key)); v != "" {
			return v
		}
	}

	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if last := strings.TrimSpace(parts[len(parts)-1]); last != "" {
			return last
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}

	return host
}

// readOrSetCookieID returns the existing _statnive cookie or mints a fresh
// UUIDv4. Real cookie strategy (httpOnly + 1y max-age + root-domain walking)
// hardens in Phase 2 + Phase 4.
func readOrSetCookieID(w http.ResponseWriter, r *http.Request) string {
	const cookieName = "_statnive"

	if c, err := r.Cookie(cookieName); err == nil && c.Value != "" {
		return c.Value
	}

	id := uuid.NewString()

	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   60 * 60 * 24 * 365,
	})

	return id
}

// Convenience: callers without a configured sites.Registry can wrap this
// to short-circuit to a fixed site_id during local dev.
type StaticSiteResolver struct {
	SiteID uint32
}

// LookupSiteIDByHostname satisfies SiteResolver.
func (s StaticSiteResolver) LookupSiteIDByHostname(_ context.Context, hostname string) (uint32, error) {
	if hostname == "" {
		return 0, sites.ErrUnknownHostname
	}

	return s.SiteID, nil
}

// emitAudit is a nil-safe wrapper so the handler test can pass Audit:nil
// and skip every audit call site without an explicit guard at each.
func emitAudit(ctx context.Context, a *audit.Logger, name audit.EventName, attrs ...slog.Attr) {
	if a == nil {
		return
	}

	a.Event(ctx, name, attrs...)
}

// truncate clips s to at most n bytes. Used to bound the size of UA
// strings written to the audit log — abuse vectors include 10-MB UAs
// designed to balloon the log file.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}

	return s[:n]
}
