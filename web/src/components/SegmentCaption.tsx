import { filtersSignal } from '../state/filters';
import './SegmentCaption.css';

// SegmentCaption renders a one-line caption summarizing the active
// property filters (Phase 5 of segments) directly under each panel's
// <h2>. Channel + path stay as chip-strip affordances; only scoped-prop
// filters (hit/session/user) surface here, because they're what changes
// the report's universe in a way the title alone doesn't convey.
//
// Renders nothing when no prop filter is active, so panels with a clean
// filter state collapse to their original layout (no reserved space).

const SCOPE_LABEL: Record<'hitProps' | 'sessionProps' | 'userProps', string> = {
  hitProps: 'Hit',
  sessionProps: 'Session',
  userProps: 'User',
};

interface SegmentCaptionProps {
  // Optional override for the lead-in. Compare uses "Comparing within"
  // because outer prop filters narrow the variant universe; every other
  // panel uses "Showing".
  lead?: string;
}

export function SegmentCaption({ lead = 'Showing' }: SegmentCaptionProps) {
  const f = filtersSignal.value;
  const parts: Array<{ scope: string; name: string; value: string }> = [];

  for (const scope of ['hitProps', 'sessionProps', 'userProps'] as const) {
    for (const [name, value] of Object.entries(f[scope])) {
      parts.push({ scope: SCOPE_LABEL[scope], name, value });
    }
  }

  if (parts.length === 0) return null;

  return (
    <p class="seg-caption" aria-label="Active property filter">
      <span class="seg-caption-lead">{lead}</span>
      {parts.map((p, i) => (
        <span key={`${p.scope}:${p.name}`} class="seg-caption-part">
          {i > 0 ? <span class="seg-caption-sep" aria-hidden="true">·</span> : null}
          <span class="seg-caption-scope">{p.scope}</span>
          <span class="seg-caption-name">{p.name}</span>
          <span class="seg-caption-eq" aria-hidden="true">=</span>
          <span class="seg-caption-value">{p.value}</span>
        </span>
      ))}
    </p>
  );
}
