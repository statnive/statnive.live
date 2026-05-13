export const fmtInt = (n: number): string => n.toLocaleString('en-US');
export const fmtPct = (n: number): string => n.toFixed(2) + '%';

// moneyFormatters memoizes Intl.NumberFormat per (currency, fractionDigits).
// Each instance is ~3 KB and triggers ICU table lookup; allocating one per
// row in a 100-row Sources table on every signal-driven render is
// gratuitous. Cardinality is bounded by the allow-list in
// internal/sites/currencies.go (~30 codes) × the two fraction-digit
// variants we use (0 for totals, 2 for RPV), so the cache stays ≤~60.
const moneyFormatters = new Map<string, Intl.NumberFormat>();

function moneyFormatter(currency: string, fractionDigits: number): Intl.NumberFormat | null {
  const key = `${currency}|${fractionDigits}`;
  const cached = moneyFormatters.get(key);
  if (cached) {
    return cached;
  }

  try {
    const f = new Intl.NumberFormat('en-US', {
      style: 'currency',
      currency,
      maximumFractionDigits: fractionDigits,
      minimumFractionDigits: fractionDigits,
    });
    moneyFormatters.set(key, f);
    return f;
  } catch {
    return null;
  }
}

// fmtMoney renders a revenue value as a currency-labelled string.
// Currency is a display-only label in this codebase — the stored
// integer is the major unit (no cents-division). The default
// fractionDigits=0 keeps revenue totals as "€1,500,000" rather than
// "€15,000.00".
//
// If `currency` is empty or not a valid ISO 4217 code (e.g. the API
// hasn't fanned out the active site yet), the function falls back to a
// plain integer + the raw code suffix so the panel still renders
// without throwing.
export const fmtMoney = (amount: number, currency: string, fractionDigits = 0): string => {
  if (!currency) {
    return fmtInt(amount);
  }

  const f = moneyFormatter(currency, fractionDigits);
  if (!f) {
    return `${fmtInt(amount)} ${currency}`;
  }

  return f.format(amount);
};

// fmtRpv renders Revenue per Visitor — the ratio revenue/visitors,
// structurally fractional. Centralises the 2-decimal policy so RPV
// call sites don't sprinkle a magic `2` and so a future change (e.g.
// JPY → 0 digits) lands in one place.
export const fmtRpv = (value: number, currency: string): string => fmtMoney(value, currency, 2);
