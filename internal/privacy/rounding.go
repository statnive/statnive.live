package privacy

// RoundToTen rounds n up to the nearest 10. Used by the dashboard
// surfacing helper when Mode.AnonymousCount() is true (consent-free /
// hybrid pre-consent) — the CNIL audience-measurement exemption
// (Sheet n°16) requires aggregated counts on anonymous data to be
// rounded so individual visitors can't be re-identified from
// time-series deltas. Returns n unchanged when n < 10 (zero is zero,
// small samples are flagged elsewhere with "<10" in the UI).
//
// Negative inputs are clamped to 0 — a negative pageview count is
// nonsensical and almost certainly an enrich-pipeline bug; clamping
// keeps the dashboard usable while the bug is hunted.
func RoundToTen(n int64) int64 {
	if n <= 0 {
		return 0
	}

	if n < 10 {
		return 10
	}

	return ((n + 5) / 10) * 10
}

// RoundCountForMode returns n unchanged unless the Mode demands
// anonymous-only counts, in which case it rounds to the nearest 10.
// Centralising this guard means every dashboard surface that takes
// (count, Mode) gets the same CNIL-safe shape with one call.
func RoundCountForMode(n int64, m Mode) int64 {
	if m.AnonymousCount() {
		return RoundToTen(n)
	}

	return n
}
