// tz.ts — derive the short timezone label for the active site's
// IANA zone name. Used by AppShell.tsx to render .statnive-tz-chip
// content reactively (e.g. "CEST" for Europe/Berlin in summer,
// "CET" in winter, "IRST" for Asia/Tehran, "UTC" for UTC).
//
// Why a dedicated helper: Intl.DateTimeFormat handles DST transitions
// automatically (Berlin is CET in January, CEST in July), so we never
// hardcode static labels. The chip then matches what the operator
// would see on their wall clock at "now".

const FALLBACK_LABEL = 'UTC';

// formatters memoises Intl.DateTimeFormat instances by IANA zone.
// Mirrors the moneyFormatters pattern in fmt.ts — each instance is
// ~3 KB and triggers an ICU table lookup on construction. Cardinality
// is bounded by the curated allow-list in internal/sites/timezones.go
// (~38 entries), so the cache stays small.
const formatters = new Map<string, Intl.DateTimeFormat>();

function getFormatter(iana: string): Intl.DateTimeFormat | null {
  const cached = formatters.get(iana);
  if (cached) return cached;

  try {
    const f = new Intl.DateTimeFormat('en-US', {
      timeZone: iana,
      timeZoneName: 'short',
    });
    formatters.set(iana, f);

    return f;
  } catch {
    // Intl throws RangeError on unknown IANA names. Defensive fallback.
    return null;
  }
}

// tzShortLabel returns the short timezone label for the given IANA
// zone at the supplied moment. Returns FALLBACK_LABEL on:
//   - empty zone name (defensive)
//   - unparseable zone name ("Garbage/Notreal")
//   - missing Intl support (server-side render, ancient browsers)
//
// The "now" argument is required so callers can render labels for
// fixed historical moments (tests) or just pass `new Date()` for the
// live header chip.
export function tzShortLabel(ianaName: string, now: Date): string {
  if (!ianaName) {
    return FALLBACK_LABEL;
  }

  const formatter = getFormatter(ianaName);
  if (!formatter) {
    return FALLBACK_LABEL;
  }

  const tzPart = formatter.formatToParts(now).find((p) => p.type === 'timeZoneName');

  return tzPart?.value ?? FALLBACK_LABEL;
}
