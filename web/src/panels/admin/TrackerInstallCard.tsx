import CopyButton from '../../components/CopyButton';

// One card, not one per row: the snippet's site_id is resolved server-
// side from the payload hostname, so a single origin-relative `<script>`
// works for every site on this installation.
//
// `data-statnive-endpoint` is explicit (not derived from `script.src`)
// so an operator reading the snippet can see where their beacons go
// without reading tracker.js source.

export function TrackerInstallCard() {
  const origin = typeof window === 'undefined' ? '' : window.location.origin;
  const snippet = `<script src="${origin}/tracker.js" data-statnive-endpoint="${origin}/api/event" async defer></script>`;

  return (
    <section class="statnive-admin-new" aria-labelledby="statnive-tracker-install-h">
      <h3 id="statnive-tracker-install-h">Tracker install</h3>
      <p class="statnive-admin-hint">
        Paste this once into every site&apos;s <code>&lt;head&gt;</code>.
        The same snippet works for all sites on this installation; the
        server resolves <code>site_id</code> from the payload hostname.
      </p>
      <pre class="statnive-admin-snippet"><code>{snippet}</code></pre>
      <CopyButton text={snippet} label="Copy snippet" />
    </section>
  );
}
