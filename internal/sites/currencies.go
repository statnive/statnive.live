package sites

// CurrencyOption is the per-row payload of GET /api/admin/currencies.
// The SPA's Add/Edit Site dropdowns render `code — symbol name` per
// option; the value sent back in PATCH /api/admin/sites/{id} is `code`.
//
// Currency is a display-only label in this codebase — there is no
// minor-unit math, no FX conversion, no historical-currency tracking.
// Switching a site's currency just changes the symbol used to render
// the existing integer values in events_raw / rollups.
type CurrencyOption struct {
	Code   string `json:"code"`   // ISO 4217 alpha-3 (USD, EUR, IRR, ...)
	Symbol string `json:"symbol"` // glyph used by Intl.NumberFormat in the SPA
	Name   string `json:"name"`   // English label for the dropdown
}

// DefaultCurrency is the new-site default + the default applied to
// existing sites by migration 007's column DEFAULT clause. Operators
// PATCH to their real currency post-deploy.
const DefaultCurrency = "EUR"

// Currencies is the canonical list rendered by the SPA dropdown and
// enforced by the server allow-list. Any code outside this set is
// rejected with ErrInvalidCurrency. v1 is 30 codes covering the
// largest-population SaaS markets; the long tail can extend in v1.1
// without a migration (currency is a free-form String column with a
// DEFAULT, not an Enum8).
var Currencies = []CurrencyOption{
	{"EUR", "€", "Euro"},
	{"USD", "$", "US Dollar"},
	{"GBP", "£", "British Pound"},
	{"CAD", "CA$", "Canadian Dollar"},
	{"AUD", "A$", "Australian Dollar"},
	{"CHF", "CHF", "Swiss Franc"},
	{"SEK", "kr", "Swedish Krona"},
	{"NOK", "kr", "Norwegian Krone"},
	{"DKK", "kr", "Danish Krone"},
	{"INR", "₹", "Indian Rupee"},
	{"AED", "د.إ", "UAE Dirham"},
	{"SAR", "ر.س", "Saudi Riyal"},
	{"TRY", "₺", "Turkish Lira"},
	{"PLN", "zł", "Polish Złoty"},
	{"CZK", "Kč", "Czech Koruna"},
	{"HUF", "Ft", "Hungarian Forint"},
	{"JPY", "¥", "Japanese Yen"},
	{"KRW", "₩", "South Korean Won"},
	{"IRR", "﷼", "Iranian Rial"},
	{"VND", "₫", "Vietnamese Đồng"},
	{"CNY", "¥", "Chinese Yuan"},
	{"HKD", "HK$", "Hong Kong Dollar"},
	{"SGD", "S$", "Singapore Dollar"},
	{"TWD", "NT$", "Taiwan Dollar"},
	{"MYR", "RM", "Malaysian Ringgit"},
	{"THB", "฿", "Thai Baht"},
	{"IDR", "Rp", "Indonesian Rupiah"},
	{"BRL", "R$", "Brazilian Real"},
	{"MXN", "MX$", "Mexican Peso"},
	{"ZAR", "R", "South African Rand"},
}

var allowedCurrencies = func() map[string]struct{} {
	m := make(map[string]struct{}, len(Currencies))
	for _, c := range Currencies {
		m[c.Code] = struct{}{}
	}

	return m
}()

// IsValidCurrency reports whether code is in the allow-list. Used by
// admin handlers to gate PATCH /api/admin/sites/{id} bodies and by
// CreateSite to validate the optional currency arg.
func IsValidCurrency(code string) bool {
	_, ok := allowedCurrencies[code]

	return ok
}
