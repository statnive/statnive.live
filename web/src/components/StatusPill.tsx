import './StatusPill.css';

// StatusPill is the reusable on/off indicator for the admin Sites,
// Users, and Goals tables. Four state values exist so calling sites
// pick the semantically correct word (Live for a site, Active for a
// user/goal); visually `live` collapses to `active` (solid green dot)
// and `paused` collapses to `disabled` (hollow ring dot).
//
// Three WCAG-2.2-AA carriers — color is never the only signal:
//   1. leading glyph (● solid / ○ hollow ring)
//   2. color  (green / none)
//   3. word label (Live / Active / Paused / Disabled)
export type StatusPillState = 'live' | 'paused' | 'active' | 'disabled';

export interface StatusPillProps {
  state: StatusPillState;
}

const LABEL: Record<StatusPillState, string> = {
  live: 'Live',
  active: 'Active',
  paused: 'Paused',
  disabled: 'Disabled',
};

// `on` = solid green dot (Live / Active). `off` = hollow rule-soft
// ring (Paused / Disabled). Two visual states, four semantic values.
function tone(state: StatusPillState): 'on' | 'off' {
  return state === 'live' || state === 'active' ? 'on' : 'off';
}

export default function StatusPill({ state }: StatusPillProps) {
  const t = tone(state);
  return (
    <span class={`statnive-status-pill is-${t}`} aria-label={`Status: ${LABEL[state]}`}>
      <span class="statnive-status-pill-dot" aria-hidden="true" />
      <span class="statnive-status-pill-label">{LABEL[state]}</span>
    </span>
  );
}
