package ingest_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/statnive/statnive.live/internal/ingest"
	"github.com/statnive/statnive.live/internal/sites"
)

// fakePipeline records every Enrich call so the test can assert the
// gate short-circuits before reaching enrichment. `last` holds a copy
// of the most recent raw event so user_id assertions can inspect what
// actually crossed the handler boundary (post hash-and-clear).
type fakePipeline struct {
	calls atomic.Int32
	last  atomic.Pointer[ingest.RawEvent]
}

func (f *fakePipeline) Enrich(raw *ingest.RawEvent) (ingest.EnrichedEvent, bool) {
	f.calls.Add(1)

	dup := *raw
	f.last.Store(&dup)

	return ingest.EnrichedEvent{
		SiteID:     raw.SiteID,
		UserIDHash: raw.UserIDHash,
		Hostname:   raw.Hostname,
		Pathname:   raw.Pathname,
	}, true
}

// fakeWAL satisfies WALSyncer for the handler tests. Records the
// enriched event so tests can inspect what the handler handed to the
// WAL path. Returns a monotonically increasing index like the real
// GroupSyncer does.
type fakeWAL struct {
	calls atomic.Int32
	last  atomic.Pointer[ingest.EnrichedEvent]
	err   error
}

func (f *fakeWAL) AppendAndWait(_ context.Context, ev ingest.EnrichedEvent) (uint64, error) {
	if f.err != nil {
		return 0, f.err
	}

	n := f.calls.Add(1)
	dup := ev
	f.last.Store(&dup)

	//nolint:gosec // atomic.Int32 monotonic counter; never negative.
	return uint64(n), nil
}

// Fast-reject gate must return 204 with zero pipeline work for prefetch
// requests and obvious bot user agents. The receiver channel must stay
// empty for every rejected case.
//
//nolint:funlen // table-driven test with 10+ cases; splitting hides the rejection matrix
func TestHandlerFastRejectGate(t *testing.T) {
	t.Parallel()

	const validUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36"

	cases := []struct {
		name       string
		method     string
		ua         string
		headers    map[string]string
		body       string
		wantStatus int
		wantPiped  bool // expect the event to land on the pipeline channel
	}{
		{
			name:       "x-purpose prefetch",
			method:     http.MethodPost,
			ua:         validUA,
			headers:    map[string]string{"X-Purpose": "prefetch"},
			body:       validBody(),
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "purpose prefetch",
			method:     http.MethodPost,
			ua:         validUA,
			headers:    map[string]string{"Purpose": "prefetch"},
			body:       validBody(),
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "x-moz prefetch",
			method:     http.MethodPost,
			ua:         validUA,
			headers:    map[string]string{"X-Moz": "prefetch"},
			body:       validBody(),
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "ua too short",
			method:     http.MethodPost,
			ua:         "tiny",
			body:       validBody(),
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "ua too long",
			method:     http.MethodPost,
			ua:         strings.Repeat("a", 600),
			body:       validBody(),
			wantStatus: http.StatusNoContent,
		},
		{
			// IPv4 UAs are short enough to be rejected by the length floor,
			// so we use IPv6 to exercise the IP-as-UA branch specifically.
			name:       "ua is ipv6",
			method:     http.MethodPost,
			ua:         "2001:0db8:85a3:0000:0000:8a2e:0370:7334",
			body:       validBody(),
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "ua is uuid",
			method:     http.MethodPost,
			ua:         "550e8400-e29b-41d4-a716-446655440000",
			body:       validBody(),
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "ua non-ascii",
			method:     http.MethodPost,
			ua:         "Mozilla/5.0 (هذا غير ASCII; long enough to pass length)",
			body:       validBody(),
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "valid ua + body",
			method:     http.MethodPost,
			ua:         validUA,
			body:       validBody(),
			wantStatus: http.StatusAccepted,
			wantPiped:  true,
		},
		{
			name:       "wrong method",
			method:     http.MethodGet,
			ua:         validUA,
			body:       "",
			wantStatus: http.StatusMethodNotAllowed,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fake := &fakePipeline{}
			wal := &fakeWAL{}

			// Production wires fast-reject as a chi middleware in front of
			// NewHandler. The handler test composes them by hand so the
			// 10-case fast-reject table still gates the right behavior.
			inner := ingest.NewHandler(ingest.HandlerConfig{
				Pipeline: fake,
				WAL:      wal,
				Sites:    ingest.StaticSiteResolver{SiteID: 1},
				Now:      func() time.Time { return time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC) },
				Logger:   slog.New(slog.DiscardHandler),
			})
			handler := ingest.FastRejectMiddleware(nil, nil)(inner)

			req := httptest.NewRequestWithContext(context.Background(), tc.method, "/api/event", strings.NewReader(tc.body))
			req.Header.Set("User-Agent", tc.ua)
			req.Header.Set("Content-Type", "text/plain")

			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}

			rr := httptest.NewRecorder()

			handler.ServeHTTP(rr, req)

			if got := rr.Code; got != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body=%q)", got, tc.wantStatus, rr.Body.String())
			}

			if tc.wantPiped {
				if calls := fake.calls.Load(); calls != 1 {
					t.Fatalf("expected 1 Enqueue call, got %d", calls)
				}
			} else {
				if calls := fake.calls.Load(); calls != 0 {
					t.Errorf("rejected request leaked into pipeline (Enqueue called %d times)", calls)
				}
			}
		})
	}
}

func validBody() string {
	return `{"hostname":"example.com","pathname":"/","event_type":"pageview","event_name":"pageview"}`
}

func mustHandle(t *testing.T, masterSecret []byte, body string) *ingest.RawEvent {
	t.Helper()

	fake := &fakePipeline{}
	wal := &fakeWAL{}
	inner := ingest.NewHandler(ingest.HandlerConfig{
		Pipeline:     fake,
		WAL:          wal,
		Sites:        ingest.StaticSiteResolver{SiteID: 1},
		MasterSecret: masterSecret,
		Now:          func() time.Time { return time.Date(2026, 4, 20, 12, 0, 0, 0, time.UTC) },
		Logger:       slog.New(slog.DiscardHandler),
	})
	handler := ingest.FastRejectMiddleware(nil, nil)(inner)

	req := httptest.NewRequest(http.MethodPost, "/api/event", strings.NewReader(body))
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
	req.Header.Set("Content-Type", "text/plain")

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}

	last := fake.last.Load()
	if last == nil {
		t.Fatal("no event reached the pipeline")
	}

	return last
}

// User ID arrives raw on the JSON wire, gets hashed to SHA-256 in the
// handler with the per-tenant master_secret, and the raw value is
// cleared before the event reaches the pipeline. Privacy Rule 4
// (CLAUDE.md) requires that the raw value never reach the WAL, the
// audit log, or events_raw.
func TestHandler_UserIDHashedAndCleared(t *testing.T) {
	t.Parallel()

	const rawUID = "user_a83f"

	masterSecret := []byte("phase-4-test-master-secret-32-bytes")

	body := `{"hostname":"example.com","pathname":"/","event_type":"custom","event_name":"signup","user_id":"` + rawUID + `"}`
	got := mustHandle(t, masterSecret, body)

	if got.UserID != "" {
		t.Errorf("raw UserID = %q; want \"\" (Privacy Rule 4 — must be cleared before pipeline)", got.UserID)
	}

	if got.UserIDHash == "" {
		t.Fatal("UserIDHash is empty; expected SHA-256 hex")
	}

	if len(got.UserIDHash) != 64 {
		t.Errorf("UserIDHash length = %d; want 64 hex chars", len(got.UserIDHash))
	}

	if strings.Contains(got.UserIDHash, rawUID) {
		t.Errorf("UserIDHash contains the raw uid: %q", got.UserIDHash)
	}
}

func TestHandler_UserIDOmittedIsEmptyHash(t *testing.T) {
	t.Parallel()

	masterSecret := []byte("phase-4-test-master-secret-32-bytes")

	got := mustHandle(t, masterSecret, validBody())

	if got.UserID != "" {
		t.Errorf("UserID = %q; want \"\"", got.UserID)
	}

	if got.UserIDHash != "" {
		t.Errorf("UserIDHash = %q; want \"\" when no user_id supplied (no spurious hash of empty string)", got.UserIDHash)
	}
}

func TestHandler_UserIDHashIsDeterministic(t *testing.T) {
	t.Parallel()

	masterSecret := []byte("phase-4-test-master-secret-32-bytes")

	body := `{"hostname":"example.com","pathname":"/","event_type":"custom","event_name":"a","user_id":"u1"}`
	a := mustHandle(t, masterSecret, body)
	b := mustHandle(t, masterSecret, body)

	if a.UserIDHash != b.UserIDHash {
		t.Errorf("hashes differ for the same (master_secret, site_id, user_id): %q != %q", a.UserIDHash, b.UserIDHash)
	}
}

// Same uid + different master_secret must yield a different hash. The
// per-tenant separation lives in HexUserIDHash via the site_id + the
// master_secret prefix; a deployment that rotates its secret produces
// a fresh identity space without code changes.
func TestHandler_UserIDHashSeparatedByMasterSecret(t *testing.T) {
	t.Parallel()

	body := `{"hostname":"example.com","pathname":"/","event_type":"custom","event_name":"a","user_id":"u1"}`

	a := mustHandle(t, []byte("secret-a-32-bytes-padding-padding"), body)
	b := mustHandle(t, []byte("secret-b-32-bytes-padding-padding"), body)

	if a.UserIDHash == b.UserIDHash {
		t.Errorf("hashes match across master_secret boundary: %q", a.UserIDHash)
	}
}

// Without a master_secret the handler stores nothing — the deployment
// has opted out of user-identity tracking entirely. Tests in the rest
// of the file all fall into this branch (they pass MasterSecret: nil).
func TestHandler_UserIDSkippedWithoutMasterSecret(t *testing.T) {
	t.Parallel()

	body := `{"hostname":"example.com","pathname":"/","event_type":"custom","event_name":"a","user_id":"u1"}`
	got := mustHandle(t, nil, body)

	if got.UserIDHash != "" {
		t.Errorf("UserIDHash = %q; want \"\" when MasterSecret is nil", got.UserIDHash)
	}

	if got.UserID != "" {
		t.Errorf("UserID = %q; want \"\" (must clear regardless of master_secret presence)", got.UserID)
	}
}

// consentOpts mirrors the operator-configurable consent flags. The
// test helper accepts these as a struct so callers can toggle each
// independently (Sec-GPC respect, DNT respect, ConsentRequired) the
// same way an operator would in YAML. Defaults are the SaaS posture:
// all three deny signals respected, ConsentRequired on.
type consentOpts struct {
	consentRequired bool
	respectGPC      bool
	respectDNT      bool
}

// Consent gate test cases — Option C: three independently-toggleable
// flags wire the SaaS-vs-Iran posture. The helper sends a single
// event and returns (hadCookie, raw) so each case can assert against
// both surfaces.
func handleWithConsent(t *testing.T, opts consentOpts, hdrs map[string]string) (bool, *ingest.RawEvent) {
	t.Helper()

	const rawUID = "user_consent"

	masterSecret := []byte("consent-test-master-secret-padded-")

	body := `{"hostname":"example.com","pathname":"/","event_type":"custom","event_name":"a","user_id":"` + rawUID + `"}`

	fake := &fakePipeline{}
	wal := &fakeWAL{}
	inner := ingest.NewHandler(ingest.HandlerConfig{
		Pipeline: fake,
		WAL:      wal,
		Sites: ingest.StaticSiteResolver{
			SiteID: 1,
			Policy: sites.SitePolicy{
				RespectDNT: opts.respectDNT,
				RespectGPC: opts.respectGPC,
				TrackBots:  true,
			},
		},
		MasterSecret:    masterSecret,
		ConsentRequired: opts.consentRequired,
		Now:             func() time.Time { return time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC) },
		Logger:          slog.New(slog.DiscardHandler),
	})
	handler := ingest.FastRejectMiddleware(nil, nil)(inner)

	req := httptest.NewRequest(http.MethodPost, "/api/event", strings.NewReader(body))
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
	req.Header.Set("Content-Type", "text/plain")

	for k, v := range hdrs {
		req.Header.Set(k, v)
	}

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}

	hadCookie := false

	for _, c := range rr.Result().Cookies() {
		if c.Name == "_statnive" {
			hadCookie = true

			break
		}
	}

	last := fake.last.Load()
	if last == nil {
		t.Fatal("no event reached the pipeline")
	}

	return hadCookie, last
}

// saasDefaults returns the EU-strict posture (all three flags true).
// Was the binary's default until the under-count diagnosis showed the
// tracker's matching client-side short-circuit was hiding 70-85% of
// legitimate traffic; the binary now defaults respect_gpc and
// respect_dnt to false (count every visit), and operators with EU
// visitors flip them on per their jurisdiction. This helper still
// represents the opted-in posture so each test case can set only the
// consentRequired axis it cares about.
func saasDefaults() consentOpts {
	return consentOpts{consentRequired: true, respectGPC: true, respectDNT: true}
}

// SaaS defaults (all 3 flags true), no signal → no cookie, no hash,
// event ingests anonymously. Privacy Rule 5.
func TestHandler_ConsentRequired_NoSignal_AnonymousIngest(t *testing.T) {
	t.Parallel()

	hadCookie, raw := handleWithConsent(t, saasDefaults(), nil)

	if hadCookie {
		t.Error("Set-Cookie sent without consent signal under SaaS defaults")
	}

	if raw.UserIDHash != "" {
		t.Errorf("UserIDHash = %q; want \"\" when consent missing", raw.UserIDHash)
	}
}

// SaaS defaults + Sec-GPC: 1 → denied even with X-Statnive-Consent
// also set. Sec-GPC wins. Privacy Rule 9.
func TestHandler_ConsentRequired_SecGPCDenies(t *testing.T) {
	t.Parallel()

	hadCookie, raw := handleWithConsent(t, saasDefaults(), map[string]string{
		"Sec-GPC":            "1",
		"X-Statnive-Consent": "given",
	})

	if hadCookie {
		t.Error("Set-Cookie sent despite Sec-GPC: 1")
	}

	if raw.UserIDHash != "" {
		t.Errorf("UserIDHash = %q; want \"\" with Sec-GPC: 1", raw.UserIDHash)
	}
}

// SaaS defaults + X-Statnive-Consent: given (no deny signal) → cookie
// + hash both populate.
func TestHandler_ConsentRequired_GivenSignal_IdentifiesVisitor(t *testing.T) {
	t.Parallel()

	hadCookie, raw := handleWithConsent(t, saasDefaults(), map[string]string{
		"X-Statnive-Consent": "given",
	})

	if !hadCookie {
		t.Error("Set-Cookie missing despite X-Statnive-Consent: given")
	}

	if raw.UserIDHash == "" {
		t.Error("UserIDHash empty despite consent given + master_secret set")
	}
}

// Iran self-hosted (ConsentRequired=false; respect flags still on by
// default for site-of-incidence DNT/GPC) → cookie + hash always set
// when no deny signal. Pre-Option-C behavior for the no-signal path.
func TestHandler_IranPosture_NoSignal_AlwaysIdentifies(t *testing.T) {
	t.Parallel()

	opts := saasDefaults()
	opts.consentRequired = false

	hadCookie, raw := handleWithConsent(t, opts, nil)

	if !hadCookie {
		t.Error("Set-Cookie missing under Iran posture with no deny signal")
	}

	if raw.UserIDHash == "" {
		t.Error("UserIDHash empty under Iran posture")
	}
}

// Iran posture + Sec-GPC: 1 → still denies because RespectGPC is on.
// Privacy Rule 9 holds regardless of operator's consent.required.
func TestHandler_IranPosture_SecGPCDenies(t *testing.T) {
	t.Parallel()

	opts := saasDefaults()
	opts.consentRequired = false

	hadCookie, raw := handleWithConsent(t, opts, map[string]string{"Sec-GPC": "1"})

	if hadCookie {
		t.Error("Set-Cookie sent despite Sec-GPC: 1")
	}

	if raw.UserIDHash != "" {
		t.Errorf("UserIDHash = %q; want \"\" with Sec-GPC: 1", raw.UserIDHash)
	}
}

// Operator unchecks RespectGPC → Sec-GPC: 1 is ignored, cookie sets.
// Used by jurisdictions where GPC has no legal weight; should be
// paired with a clear in-product disclosure.
func TestHandler_RespectGPCUnchecked_GPCIgnored(t *testing.T) {
	t.Parallel()

	opts := saasDefaults()
	opts.consentRequired = false // also drop ConsentRequired so cookie can set
	opts.respectGPC = false

	hadCookie, _ := handleWithConsent(t, opts, map[string]string{"Sec-GPC": "1"})

	if !hadCookie {
		t.Error("Set-Cookie missing despite RespectGPC=false unchecking the deny")
	}
}

// DNT: 1 denies under EU-strict opt-in (RespectDNT=true). LEARN.md
// Lesson 16. Tracker JS no longer short-circuits client-side, so this
// is the only DNT enforcement path in the binary.
func TestHandler_DNTDenies(t *testing.T) {
	t.Parallel()

	hadCookie, raw := handleWithConsent(t, saasDefaults(), map[string]string{"DNT": "1"})

	if hadCookie {
		t.Error("Set-Cookie sent despite DNT: 1 with RespectDNT=true")
	}

	if raw.UserIDHash != "" {
		t.Errorf("UserIDHash = %q; want \"\" with DNT: 1", raw.UserIDHash)
	}
}

// Operator unchecks RespectDNT → DNT: 1 is ignored.
func TestHandler_RespectDNTUnchecked_DNTIgnored(t *testing.T) {
	t.Parallel()

	opts := saasDefaults()
	opts.consentRequired = false
	opts.respectDNT = false

	hadCookie, _ := handleWithConsent(t, opts, map[string]string{"DNT": "1"})

	if !hadCookie {
		t.Error("Set-Cookie missing despite RespectDNT=false unchecking the deny")
	}
}
