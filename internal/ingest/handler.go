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
	"github.com/statnive/statnive.live/internal/metrics"
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
//
// LookupSitePolicy returns the site_id + per-site privacy posture in a
// single round-trip — the hot ingest path requires both to gate consent
// and bot tracking per-tenant (migration 006 + LEARN.md Lesson 24
// follow-up). LookupSiteIDByHostname is kept for non-ingest callers.
type SiteResolver interface {
	LookupSiteIDByHostname(ctx context.Context, hostname string) (uint32, error)
	LookupSitePolicy(ctx context.Context, hostname string) (uint32, sites.SitePolicy, error)
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
	// ConsentRequired gates the _statnive cookie + user_id hashing behind
	// an explicit consent signal (X-Statnive-Consent: given). Default
	// true on the SaaS binary (EU posture); operators of self-hosted
	// Iran tiers (no GDPR) flip to false. Maps to CLAUDE.md Privacy
	// Rule 5 (SaaS = GDPR applies; Iran = cookies allowed).
	ConsentRequired bool
	// Metrics bumps Prometheus-text counters at every received / accepted
	// / dropped point. Optional — nil-safe; production sets it, tests can
	// pass nil. The funnel breakdown (received - accepted - sum(dropped))
	// is the canonical diagnostic surface for under-count complaints.
	Metrics *metrics.Registry
	// Suppression silently drops events whose hashed cookie_id appears
	// on the in-memory opt-out list (Stage 2). Optional — nil disables
	// the check, which is the correct posture for the Iran-self-hosted
	// tier that never serves /api/privacy/opt-out.
	Suppression SuppressionChecker

	// Mode resolves the per-request consent posture (Stage 3). nil
	// disables every mode-derived gate — the handler behaves like
	// Stage-2 ModeCurrent. Production wires privacy.PolicyToMode.
	Mode ModeResolver
}

// SuppressionChecker is the minimum interface the ingest gate calls
// to honour visitor opt-outs. Implemented by *privacy.SuppressionList
// in production; tests inject a stub. Defined here (not imported from
// internal/privacy) so the dependency flows one-way and the future
// privacy package can grow without dragging ingest into a cycle.
type SuppressionChecker interface {
	IsSuppressed(hash string) bool
}

// ModeResolver maps a request + policy to the consent posture under
// which the event should be ingested. Production uses
// privacy.PolicyToMode; tests inject a fixed-Mode stub. Mode itself
// is an interface so the ingest package doesn't take a hard import
// dep on internal/privacy (and so the surface stays narrow — only
// the two predicates the gate actually queries).
type ModeResolver func(r *http.Request, p sites.SitePolicy) Mode

// Mode is the consent posture the ingest gate queries. privacy.Mode
// satisfies this shape at the call site in cmd/statnive-live/main.go.
type Mode interface {
	AllowsIdentifier() bool
	EnforcesEventAllowlist() bool
}

// eventNameFor returns the effective event_name for an event, falling
// back to event_type when the tracker omitted event_name (matches the
// enrich pipeline's eventName-or-eventType collapse).
func eventNameFor(e *RawEvent) string {
	if e.EventName != "" {
		return e.EventName
	}

	return e.EventType
}

// eventNameAllowed reports whether name appears in the allow-list.
// Empty allow-list is treated as "no enforcement" so a misconfigured
// site can't lose every event silently (Privacy-Rule-9 style: fail
// open at ingest, fail loud in admin UI).
func eventNameAllowed(name string, allowlist []string) bool {
	if len(allowlist) == 0 {
		return true
	}

	for _, a := range allowlist {
		if a == name {
			return true
		}
	}

	return false
}

// NewHandler returns the http.Handler wired for POST /api/event.
func NewHandler(cfg HandlerConfig) http.Handler {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	hashIdentity := len(cfg.MasterSecret) > 0

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serve(w, r, cfg, hashIdentity)
	})
}

//nolint:gocyclo // PR D2 added per-site policy lookup + lazy cookie gate inside the events loop, bumping cyclomatic complexity from 12 to 14. Splitting helpers out would force several params (cfg, hashIdentity, cookieID) through call sites that don't need them and obscure the linear request flow.
func serve(w http.ResponseWriter, r *http.Request, cfg HandlerConfig, hashIdentity bool) {
	// FastRejectMiddleware enforces POST-only + the prefetch/UA-shape
	// fast-reject before any downstream middleware. By the time we get
	// here the request has passed both checks and the rate limiter.
	ua := r.Header.Get("User-Agent")

	cfg.Metrics.IncReceived()

	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

	defer func() { _ = r.Body.Close() }()

	events, err := parseBody(r.Body)
	if err != nil {
		cfg.Logger.Debug("body parse failed", "err", err)
		cfg.Metrics.IncDropped(parseErrorReason(err))
		http.Error(w, "bad request", http.StatusBadRequest)

		return
	}

	if len(events) == 0 {
		cfg.Metrics.IncDropped(metrics.ReasonEmptyBody)
		w.WriteHeader(http.StatusNoContent)

		return
	}

	now := cfg.Now().UTC()
	ip := ClientIP(r)

	// Cookie is set lazily — the per-site consent gate (Privacy Rules 5 +
	// 9) runs INSIDE the events loop because each event's hostname
	// resolves to its own site_id + policy. We mint the cookie at most
	// once per request and only after the first event whose policy
	// allows identity.
	var cookieID string

	for i := range events {
		raw := &events[i]

		// Server-authoritative — TSUTC, UserAgent, IP, CookieID are
		// json:"-" on RawEvent. Trust the request, not the body.
		raw.TSUTC = now
		raw.UserAgent = ua
		raw.IP = ip

		siteID, policy, sErr := cfg.Sites.LookupSitePolicy(r.Context(), raw.Hostname)
		if sErr != nil {
			cfg.Logger.Debug("unknown hostname", "hostname", raw.Hostname)
			emitAudit(r.Context(), cfg.Audit, audit.EventHostnameUnknown,
				slog.String("hostname", raw.Hostname),
			)
			cfg.Metrics.IncDropped(metrics.ReasonHostnameUnknown)
			// Drop unknown-hostname events silently with 204 — doc 24 calls out
			// this is what trackers expect. Bot scrapers will see no signal.
			w.WriteHeader(http.StatusNoContent)

			return
		}

		raw.SiteID = siteID

		// Stage-3 consent-mode gate. Mode is derived from the site's
		// SitePolicy + the request's _statnive_consent cookie /
		// X-Statnive-Consent header. The Mode predicates encapsulate
		// every Stage-3 enforcement rule so this handler stays
		// hostname → policy → mode → ingest, no inline switch.
		var mode Mode
		if cfg.Mode != nil {
			mode = cfg.Mode(r, policy)
		}

		// Per-site consent gate. Sec-GPC / DNT short-circuit per
		// statnive.sites.respect_gpc / respect_dnt (default 0 — opt-in
		// per CLAUDE.md Privacy Rule 6). When the operator's site is
		// EU-strict (respect_gpc=1) and the visitor sends Sec-GPC: 1,
		// identity is suppressed but the visit still ingests anonymously.
		//
		// Layered on top: when Mode says AllowsIdentifier()=false
		// (consent-free, consent-required without consent, hybrid
		// pre-consent) the cookie is refused regardless of the legacy
		// per-site flags. allowIdentity is the AND of both gates so
		// the stricter rule always wins.
		allowIdentity := !consentDenied(r, policy) &&
			(!cfg.ConsentRequired || consentGiven(r))

		if mode != nil && !mode.AllowsIdentifier() {
			allowIdentity = false
		}

		if allowIdentity && cookieID == "" {
			cookieID = readOrSetCookieID(w, r)
		}

		// _statnive cookie hashed before the pipeline / WAL / batch
		// writer can see it. The raw UUID stays in the Set-Cookie
		// response so cross-session continuity in the browser still
		// works; only the at-rest events_raw.cookie_id carries the
		// SHA-256 hash. hashIdentity=false (master secret absent —
		// tests only) wipes the field rather than leaking the raw UUID,
		// matching the user_id path below.
		if hashIdentity && cookieID != "" {
			raw.CookieID = identity.HexCookieIDHash(cfg.MasterSecret, raw.SiteID, cookieID)
		} else {
			raw.CookieID = ""
		}

		// Visitor opt-out gate (Stage 2 — GDPR Art. 21). If the
		// suppression list contains the hashed cookie_id, drop the
		// event silently with 204 and skip every downstream stage.
		// We MUST NOT leak whether a visitor is suppressed (would
		// undermine the right to object); the response shape is
		// identical to a normal "accepted" path.
		if cfg.Suppression != nil && raw.CookieID != "" && cfg.Suppression.IsSuppressed(raw.CookieID) {
			cfg.Metrics.IncDropped(metrics.ReasonOptedOut)

			continue
		}

		// Stage-3 event-allowlist gate. When Mode says
		// EnforcesEventAllowlist()=true (consent-free, hybrid pre-
		// consent) only event_name values in policy.EventAllowlist
		// are accepted — matches the CNIL audience-measurement
		// exemption (Sheet n°16) 3-event ceiling. Drop is silent
		// for the same reason as opt-out: the visitor must not be
		// able to enumerate the operator's allow-list.
		if mode != nil && mode.EnforcesEventAllowlist() &&
			!eventNameAllowed(eventNameFor(raw), policy.EventAllowlist) {
			cfg.Metrics.IncDropped(metrics.ReasonEventNotAllowed)

			continue
		}

		// Hash user_id with the per-tenant key material, then wipe the
		// raw value before it can reach the pipeline / WAL / batch
		// writer. Privacy Rule 4: only SHA-256(master_secret || site_id
		// || user_id) ever lands in events_raw.user_id_hash. If
		// hashIdentity is false (no master_secret configured), the raw
		// value is still cleared — silently dropping the uid is the
		// stricter privacy stance. allowIdentity gates hashing per
		// Privacy Rule 9 (Sec-GPC + consent decline short-circuit
		// BEFORE hash computation, not after).
		if hashIdentity && allowIdentity && raw.UserID != "" {
			raw.UserIDHash = identity.HexUserIDHash(cfg.MasterSecret, raw.SiteID, raw.UserID)
		}

		raw.UserID = ""

		// Per-site bot policy. Default track_bots=true keeps today's
		// behavior (bots are flagged is_bot=1 + ingest). When the
		// operator flips to false, the pipeline drops bot events with
		// metrics.ReasonBotDropped — useful for sites that don't want
		// bot traffic in their HLL state at all.
		raw.TrackBots = policy.TrackBots

		// Synchronous 6-stage enrichment runs on the handler goroutine.
		// Burst-dropped events skip the WAL silently — they're a known
		// rejection class, not a server failure. Pipeline bumps the
		// burst_dropped (or bot_dropped) counter itself; we don't
		// double-count here.
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
			cfg.Metrics.IncDropped(metrics.ReasonWALSyncError)
			http.Error(w, "service unavailable", http.StatusServiceUnavailable)

			return
		}

		cfg.Metrics.IncAccepted(raw.SiteID)
	}

	w.WriteHeader(http.StatusAccepted)
}

// parseErrorReason classifies a parseBody error into a metrics drop label.
// MaxBytesReader returns *http.MaxBytesError when the body exceeds 8 KB;
// everything else is treated as a JSON shape problem. Both surface as 400
// to the client but get distinct counter labels in the funnel.
func parseErrorReason(err error) string {
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return metrics.ReasonPayloadTooLarge
	}

	return metrics.ReasonJSONInvalid
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

// consentDenied reports whether the visitor's browser is signaling
// "do not process for personalization" through any of the deny
// signals the per-site policy has flipped on. Privacy Rule 9 (Sec-GPC)
// and LEARN.md Lesson 16 (DNT) both wired through statnive.sites
// columns so a multi-tenant operator can serve EU + non-EU customers
// from the same binary without re-editing config.
func consentDenied(r *http.Request, policy sites.SitePolicy) bool {
	if policy.RespectGPC && r.Header.Get("Sec-GPC") == "1" {
		return true
	}

	if policy.RespectDNT && r.Header.Get("DNT") == "1" {
		return true
	}

	return false
}

// consentGiven reports whether the visitor's site has signaled
// affirmative consent (e.g. via a CMP integration that flips the
// X-Statnive-Consent header to "given"). Used only when
// HandlerConfig.ConsentRequired is true; ignored otherwise.
func consentGiven(r *http.Request) bool {
	return r.Header.Get("X-Statnive-Consent") == "given"
}

// readOrSetCookieID returns the existing _statnive cookie or mints a fresh
// UUIDv4. Real cookie strategy (httpOnly + 1y max-age + root-domain walking)
// hardens in Phase 2 + Phase 4. Caller gates on consent; this fn unconditionally
// sets the cookie when invoked.
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

// StaticSiteResolver is a convenience resolver for callers without a
// configured sites.Registry — short-circuits to a fixed site_id during
// local dev. Returns the zero-value SitePolicy (default-off DNT/GPC,
// track_bots=true), which matches the post-PR-#78 SaaS posture.
type StaticSiteResolver struct {
	SiteID uint32
	Policy sites.SitePolicy
}

// LookupSiteIDByHostname satisfies SiteResolver.
func (s StaticSiteResolver) LookupSiteIDByHostname(_ context.Context, hostname string) (uint32, error) {
	if hostname == "" {
		return 0, sites.ErrUnknownHostname
	}

	return s.SiteID, nil
}

// LookupSitePolicy satisfies SiteResolver. Returns the resolver's
// preset Policy or the zero value (RespectDNT=false, RespectGPC=false,
// TrackBots=false). Callers that want the production default of
// TrackBots=true should set it explicitly on the resolver.
func (s StaticSiteResolver) LookupSitePolicy(_ context.Context, hostname string) (uint32, sites.SitePolicy, error) {
	if hostname == "" {
		return 0, sites.SitePolicy{}, sites.ErrUnknownHostname
	}

	return s.SiteID, s.Policy, nil
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
