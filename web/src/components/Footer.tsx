import { useEffect } from 'preact/hooks';
import { useSignal } from '@preact/signals';
import './Footer.css';

interface AboutResponse {
  version: string;
  git_sha: string;
  go_version: string;
}

// Footer is the single place on the dashboard that carries the third
// attribution surface required by CLAUDE.md License Rules for
// IP2Location LITE CC-BY-SA-4.0. The other two surfaces are
// LICENSE-third-party.md (ships in the bundle) and GET /api/about
// (served by the binary).
//
// The short "GeoIP by IP2Location LITE" string is the inline UI form;
// the verbatim CC-BY-SA text lives at /api/about (a link below).
//
// Version string comes from the same /api/about response so operators
// can eyeball the running binary's build without a systemctl query.
export function Footer() {
  const version = useSignal<string>('');

  useEffect(() => {
    const ac = new AbortController();

    (async () => {
      try {
        const r = await fetch('/api/about', { signal: ac.signal });
        if (!r.ok) return;

        const data = (await r.json()) as AboutResponse;
        if (data.version) {
          version.value = data.version;
        }
      } catch {
        // Silent — the footer still renders the attribution; version
        // stays empty. /api/about is unauthenticated and shouldn't
        // plausibly fail, but a dev environment might not have
        // /api/about wired.
      }
    })();

    return () => ac.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return (
    <footer class="statnive-footer" role="contentinfo">
      <span class="statnive-footer-brand">
        statnive{version.value ? ` · ${version.value}` : ''}
      </span>
      <span class="statnive-footer-sep">·</span>
      <a class="statnive-footer-link" href="/api/about">
        About
      </a>
      <span class="statnive-footer-sep">·</span>
      <span class="statnive-footer-attribution">
        GeoIP by{' '}
        <a href="https://lite.ip2location.com" rel="noopener noreferrer">
          IP2Location LITE
        </a>
      </span>
    </footer>
  );
}
