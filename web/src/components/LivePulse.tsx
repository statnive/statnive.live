import './LivePulse.css';

// LivePulse renders the 8px green dot + 2s pulse that marks "realtime
// is polling". Pure CSS animation; `prefers-reduced-motion: reduce`
// disables the keyframes via media query in LivePulse.css. Used next
// to the active Realtime tab in Nav.tsx and the Realtime panel heading.
export function LivePulse(props: { 'aria-label'?: string }) {
  return (
    <span
      class="statnive-live-pulse"
      role="status"
      aria-label={props['aria-label'] ?? 'Live'}
    />
  );
}
