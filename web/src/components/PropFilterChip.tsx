import { removePropFilter } from '../state/filters';
import './PropFilterChip.css';

// Phase 5 of segments — applied-filter chip. Renders one filter as a
// pill with a scope icon (shape, not colour, for CVD safety), scope
// label, prop name, value, and a remove × button. aria-label carries
// the long-form description so screen readers announce the full filter.

const SCOPE_LABELS: Record<'hitProps' | 'sessionProps' | 'userProps', string> = {
  hitProps: 'Hit',
  sessionProps: 'Session',
  userProps: 'User',
};

// Shape-distinguished icons per plan § 11.2 — circle/diamond/square so
// scope encoding survives WCAG SC 1.4.1 (use of colour).
function ScopeIcon({ scope }: { scope: 'hitProps' | 'sessionProps' | 'userProps' }) {
  if (scope === 'hitProps') {
    return (
      <svg width="8" height="8" viewBox="0 0 8 8" aria-hidden="true" class="seg-chip-icon">
        <circle cx="4" cy="4" r="3" fill="currentColor" />
      </svg>
    );
  }
  if (scope === 'sessionProps') {
    return (
      <svg width="8" height="8" viewBox="0 0 8 8" aria-hidden="true" class="seg-chip-icon">
        <polygon points="4,0 8,4 4,8 0,4" fill="currentColor" />
      </svg>
    );
  }
  return (
    <svg width="8" height="8" viewBox="0 0 8 8" aria-hidden="true" class="seg-chip-icon">
      <rect x="1" y="1" width="6" height="6" fill="currentColor" />
    </svg>
  );
}

interface PropFilterChipProps {
  scope: 'hitProps' | 'sessionProps' | 'userProps';
  name: string;
  value: string;
}

export function PropFilterChip({ scope, name, value }: PropFilterChipProps) {
  const scopeLabel = SCOPE_LABELS[scope];
  const ariaLabel = `Remove filter: ${scopeLabel} scope, ${name} equals ${value}.`;

  return (
    <button
      type="button"
      class="seg-chip"
      data-scope={scope}
      onClick={() => removePropFilter(scope, name)}
      aria-label={ariaLabel}
    >
      <ScopeIcon scope={scope} />
      <span class="seg-chip-scope">{scopeLabel}</span>
      <span class="seg-chip-name">{name}</span>
      <span class="seg-chip-sep" aria-hidden="true">:</span>
      <span class="seg-chip-value">{value}</span>
      <span class="seg-chip-remove" aria-hidden="true">×</span>
    </button>
  );
}
