package sites

import "testing"

// TestCurrencies_AllowListMatchesSlice asserts that every entry in the
// public Currencies slice round-trips through IsValidCurrency. Catches
// drift if a future PR adds a CurrencyOption row but forgets to extend
// the allowedCurrencies map (or vice versa).
func TestCurrencies_AllowListMatchesSlice(t *testing.T) {
	t.Parallel()

	if len(Currencies) == 0 {
		t.Fatal("Currencies slice is empty")
	}

	if len(allowedCurrencies) != len(Currencies) {
		t.Errorf("allowedCurrencies len = %d, Currencies len = %d", len(allowedCurrencies), len(Currencies))
	}

	for _, c := range Currencies {
		if !IsValidCurrency(c.Code) {
			t.Errorf("Currencies[%q] missing from allowedCurrencies", c.Code)
		}

		if c.Symbol == "" {
			t.Errorf("Currencies[%q] has empty Symbol", c.Code)
		}

		if c.Name == "" {
			t.Errorf("Currencies[%q] has empty Name", c.Code)
		}
	}
}

// TestIsValidCurrency_Rejects asserts the negative path: codes outside
// the allow-list must be rejected. The handler maps this to HTTP 400.
func TestIsValidCurrency_Rejects(t *testing.T) {
	t.Parallel()

	for _, code := range []string{"", "FOO", "eur", "Euro", "EURO", "XYZ"} {
		if IsValidCurrency(code) {
			t.Errorf("IsValidCurrency(%q) = true, want false", code)
		}
	}
}

// TestIsValidCurrency_Accepts pins the contract that the headline
// currencies (default + the most-likely operator picks for SaaS day 1)
// are accepted. If any of these stop validating the SPA dropdown
// breaks for a real customer.
func TestIsValidCurrency_Accepts(t *testing.T) {
	t.Parallel()

	for _, code := range []string{"EUR", "USD", "GBP", "JPY", "IRR", "CNY", "INR"} {
		if !IsValidCurrency(code) {
			t.Errorf("IsValidCurrency(%q) = false, want true", code)
		}
	}
}

// TestDefaultCurrency_IsValid catches the regression where someone
// renames DefaultCurrency without updating the Currencies slice.
// CreateSite would then return ErrInvalidCurrency for the very
// default it falls back to.
func TestDefaultCurrency_IsValid(t *testing.T) {
	t.Parallel()

	if !IsValidCurrency(DefaultCurrency) {
		t.Errorf("DefaultCurrency %q not in allow-list", DefaultCurrency)
	}

	if DefaultCurrency != "EUR" {
		t.Errorf("DefaultCurrency = %q, want EUR (per design decision)", DefaultCurrency)
	}
}
