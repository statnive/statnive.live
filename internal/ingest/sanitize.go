package ingest

import (
	"regexp"
	"unicode/utf8"

	"github.com/statnive/statnive.live/internal/metrics"
)

// Per-field caps for the ingest XSS-hardening gate. These are generous
// enough that no legitimate tracker payload trips them; they exist as a
// defense-in-depth backstop so a future read-side endpoint that touches
// raw event fields cannot become an XSS sink (Architecture Rule 1 makes
// the raw table write-only today — these caps make the *contents* safe
// even if that rule ever gets relaxed).
const (
	maxTitleLen       = 512  // realistic <title> is <200 chars; ZSTD-compressed at rest.
	maxReferrerLen    = 1024 // longest plausible URL referrer.
	maxPathnameLen    = 1024
	maxHostnameLen    = 253 // DNS label spec ceiling.
	maxUTMFieldLen    = 256
	maxUserSegmentLen = 64
	maxPropsEntries   = 50
	maxPropValLen     = 256
)

// eventNameRe matches name-shaped tokens: letters, digits, _, -, ., :, /.
// Allows the GA4 + Plausible + Umami event-name conventions, plus "/" for
// path-style names ("checkout/step-1"). Forbids whitespace, HTML brackets,
// quotes, ampersand — anything that would round-trip dangerously through
// HTML or JSON-in-script-tag. Used for both event_name and event_type
// since both follow the same shape.
var eventNameRe = regexp.MustCompile(`^[A-Za-z0-9_.\-:/]{1,64}$`)

// propKeyRe is tighter than eventNameRe — no "/" or ":" because prop_keys
// are stored as Array(String) and round-tripped as JSON object keys
// downstream where path-style separators get awkward.
var propKeyRe = regexp.MustCompile(`^[A-Za-z0-9_.\-]{1,40}$`)

// ValidateAndSanitize is the per-event ingest gate. It runs in the
// handler's per-event loop after LookupSitePolicy succeeds, so the drop
// metric is attributable to a site_id.
//
// Two postures:
//
//   - Hard-reject (returns a non-empty reason) for charset violations on
//     event_name / event_type / prop keys. These fields are name-shaped
//     tokens by spec — a "<script>" in event_name is never a tracker
//     bug, it is an attack or a broken integration that the operator
//     wants to know about. Hard-rejects run BEFORE the free-field
//     sanitization so rejected events skip the 10 sanitizeFree calls.
//   - Soft-sanitize (truncate to the per-field cap, strip C0 control
//     chars except \t and DEL) for free-form text fields: title,
//     referrer, pathname, hostname, utm_*, user_segment, prop values.
//     These come from <title>, document.referrer, location.pathname,
//     etc. — malformed values are far more often broken trackers than
//     attacks, and the dashboard does not render them today.
//
// The function mutates *ev in place to avoid a ~280-byte struct copy on
// the ingest hot path (40K EPS design ceiling per CLAUDE.md). Map values
// in ev.Props are also rewritten in place when sanitizeFree shortens
// them. Caller pattern (handler.go per-event loop):
//
//	if vReason := ValidateAndSanitize(raw); vReason != "" {
//	    cfg.Metrics.IncDropped(vReason)
//	    continue
//	}
//
// Empty event_name / event_type is accepted — eventNameFor (event.go)
// and the events_raw schema both default to "pageview".
func ValidateAndSanitize(ev *RawEvent) string {
	if ev.EventName != "" && !eventNameRe.MatchString(ev.EventName) {
		return metrics.ReasonBadEventName
	}

	if ev.EventType != "" && !eventNameRe.MatchString(ev.EventType) {
		return metrics.ReasonBadEventType
	}

	if len(ev.Props) > maxPropsEntries {
		return metrics.ReasonTooManyProps
	}

	for k := range ev.Props {
		if !propKeyRe.MatchString(k) {
			return metrics.ReasonBadPropKey
		}
	}

	ev.Hostname = sanitizeFree(ev.Hostname, maxHostnameLen)
	ev.Pathname = sanitizeFree(ev.Pathname, maxPathnameLen)
	ev.Title = sanitizeFree(ev.Title, maxTitleLen)
	ev.Referrer = sanitizeFree(ev.Referrer, maxReferrerLen)
	ev.UTMSource = sanitizeFree(ev.UTMSource, maxUTMFieldLen)
	ev.UTMMedium = sanitizeFree(ev.UTMMedium, maxUTMFieldLen)
	ev.UTMCampaign = sanitizeFree(ev.UTMCampaign, maxUTMFieldLen)
	ev.UTMContent = sanitizeFree(ev.UTMContent, maxUTMFieldLen)
	ev.UTMTerm = sanitizeFree(ev.UTMTerm, maxUTMFieldLen)
	ev.UserSegment = sanitizeFree(ev.UserSegment, maxUserSegmentLen)

	for k, v := range ev.Props {
		cleaned := sanitizeFree(v, maxPropValLen)
		if cleaned != v {
			ev.Props[k] = cleaned
		}
	}

	return ""
}

// sanitizeFree strips C0 control characters (U+0000–U+001F except \t) and
// DEL (U+007F), then truncates at the last full UTF-8 rune boundary so
// the result is ≤ maxBytes bytes. Multibyte runes (Unicode in titles,
// referrers, prop values) are preserved intact — we never split a rune.
//
// `<`, `>`, `&`, quotes, etc. are NOT stripped: they are valid bytes in
// a URL referrer or a page title, and the dashboard layer is responsible
// for HTML-escaping on render. This function is about ensuring the
// *stored* bytes are bounded and printable, not about pre-escaping
// HTML — that would be the wrong abstraction layer per OWASP A03.
func sanitizeFree(s string, maxBytes int) string {
	if s == "" {
		return s
	}

	if len(s) <= maxBytes && !hasControlChar(s) {
		return s
	}

	b := make([]byte, 0, min(len(s), maxBytes))

	for _, r := range s {
		if isControl(r) {
			continue
		}

		size := utf8.RuneLen(r)
		if size < 0 {
			continue
		}

		if len(b)+size > maxBytes {
			break
		}

		b = utf8.AppendRune(b, r)
	}

	return string(b)
}

// hasControlChar reports whether s contains any C0 control character
// (except \t) or DEL. Used to short-circuit sanitizeFree on the common
// case of already-clean strings.
func hasControlChar(s string) bool {
	for i := range len(s) {
		c := s[i]
		if c < 0x20 && c != '\t' {
			return true
		}

		if c == 0x7F {
			return true
		}
	}

	return false
}

// isControl reports whether r is a C0 control character (except \t) or
// DEL. \r and \n are stripped because they enable log-injection / CRLF
// header smuggling if a value ever round-trips through a non-JSON
// response.
func isControl(r rune) bool {
	if r == '\t' {
		return false
	}

	return r < 0x20 || r == 0x7F
}

// EventNameMatchPattern returns the regex source that ValidateAndSanitize
// uses for event_name / event_type charset enforcement. Exported so the
// pre-deploy sanity sweep documented in the XSS-hardening plan can run
// the equivalent ClickHouse `match()` against events_raw before flipping
// the gate on, without hard-coding the pattern in two places.
//
// The canonical CH probe is:
//
//	SELECT event_name FROM statnive.events_raw
//	WHERE event_name != '' AND NOT match(event_name, '^[A-Za-z0-9_.\\-:/]+$')
//	LIMIT 20
func EventNameMatchPattern() string { return eventNameRe.String() }

// PropKeyMatchPattern is the analogous accessor for the prop_keys regex.
// See EventNameMatchPattern for the pre-deploy CH probe pattern.
func PropKeyMatchPattern() string { return propKeyRe.String() }
