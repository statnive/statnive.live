export const fmtInt = (n: number): string => n.toLocaleString('en-US');
export const fmtPct = (n: number): string => n.toFixed(2) + '%';

// moneyFormatters memoizes Intl.NumberFormat per currency code. Each
// instance is ~3 KB and triggers ICU table lookup; allocating one per
// row in a 100-row Sources table on every signal-driven render is
// gratuitous. Cardinality is bounded by the allow-list in
// internal/sites/currencies.go (~30 codes), so the cache never grows.
const moneyFormatters = new Map<string, Intl.NumberFormat>();

function moneyFormatter(currency: string): Intl.NumberFormat | null {
  const cached = moneyFormatters.get(currency);
  if (cached) {
    return cached;
  }

  try {
    const f = new Intl.NumberFormat('en-US', {
      style: 'currency',
      currency,
      maximumFractionDigits: 0,
      minimumFractionDigits: 0,
    });
    moneyFormatters.set(currency, f);
    return f;
  } catch {
    return null;
  }
}

// fmtMoney renders an integer revenue value as a currency-labelled
// string. Currency is a display-only label in this codebase — the
// stored integer is the major unit (no cents-division). The
// maximumFractionDigits / minimumFractionDigits = 0 pair forces
// 2-decimal currencies (EUR, USD) to render as "€1,500,000" rather
// than "€15,000.00", and zero-decimal currencies (JPY, IRR) keep
// their natural shape. If `currency` is empty or not a valid ISO 4217
// code (e.g. the API hasn't fanned out the active site yet), the
// function falls back to a plain integer + the raw code suffix so the
// panel still renders without throwing.
export const fmtMoney = (amount: number, currency: string): string => {
  if (!currency) {
    return fmtInt(amount);
  }

  const f = moneyFormatter(currency);
  if (!f) {
    return `${fmtInt(amount)} ${currency}`;
  }

  return f.format(amount);
};
