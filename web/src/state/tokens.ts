// tokens.ts reads brand token values from the tokens.css custom
// properties at runtime. Exists so JS consumers (uPlot series stroke,
// canvas fills) can reference brand colors without inlining hex
// literals — which would fail `make brand-grep`.
//
// Falls back to the documented docs/brand.md values for environments
// without a DOM (vitest with early test setup); prod always reads the
// live CSS var so swapping tokens.css propagates to canvas too.

interface BrandTokens {
  green: string;
  greenDark: string;
  greenLight: string;
  ink: string;
  paper: string;
  ochre: string;
  ruleSoft: string;
}

const FALLBACK: BrandTokens = {
  green: 'var(--green)',
  greenDark: 'var(--green-dk)',
  greenLight: 'var(--green-lt)',
  ink: 'var(--ink)',
  paper: 'var(--paper)',
  ochre: 'var(--ochre)',
  ruleSoft: 'var(--rule-soft)',
};

function readVar(name: string, fallback: string): string {
  if (typeof document === 'undefined') return fallback;
  const root = document.getElementById('statnive-app') ?? document.documentElement;
  const v = getComputedStyle(root).getPropertyValue(name).trim();
  return v || fallback;
}

// Reads fresh every call so a future dark-mode swap picks up the new
// values without re-mounting every chart.
export function readBrandTokens(): BrandTokens {
  return {
    green: readVar('--green', FALLBACK.green),
    greenDark: readVar('--green-dk', FALLBACK.greenDark),
    greenLight: readVar('--green-lt', FALLBACK.greenLight),
    ink: readVar('--ink', FALLBACK.ink),
    paper: readVar('--paper', FALLBACK.paper),
    ochre: readVar('--ochre', FALLBACK.ochre),
    ruleSoft: readVar('--rule-soft', FALLBACK.ruleSoft),
  };
}
