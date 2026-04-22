import { useEffect } from 'preact/hooks';
import { useSignal } from '@preact/signals';
import { apiGet } from '../api/client';
import type { RealtimeResponse } from '../api/types';
import { realtimeTickSignal } from '../state/realtime';
import { siteSignal } from '../state/site';
import { fmtInt } from '../lib/fmt';
import './panels.css';

export default function Realtime() {
  const data = useSignal<RealtimeResponse | null>(null);
  const err = useSignal<string | null>(null);

  useEffect(() => {
    err.value = null;
    const ac = new AbortController();

    (async () => {
      try {
        data.value = await apiGet<RealtimeResponse>('/api/realtime/visitors', {}, ac.signal);
      } catch (e: unknown) {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        err.value = e instanceof Error ? e.message : String(e);
      }
    })();

    return () => ac.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [realtimeTickSignal.value, siteSignal.value]);

  if (err.value) {
    return (
      <section class="statnive-section">
        <h2 class="statnive-h2">Realtime</h2>
        <p class="statnive-error">could not load — see logs</p>
      </section>
    );
  }

  const d = data.value;

  return (
    <section class="statnive-section" data-testid="panel-realtime">
      <h2 class="statnive-h2">Realtime</h2>
      <div class="statnive-realtime">
        <div class="statnive-realtime-card">
          <div class="statnive-label">Active visitors (current hour)</div>
          <div class="statnive-realtime-big" data-testid="realtime-active">
            {d ? fmtInt(d.active_visitors) : '—'}
          </div>
        </div>
        <div class="statnive-realtime-card">
          <div class="statnive-label">Pageviews last hour</div>
          <div class="statnive-realtime-med">
            {d ? fmtInt(d.pageviews_last_hr) : '—'}
          </div>
        </div>
      </div>
      <p class="statnive-meta">
        auto-refresh every 10s · pauses when tab is hidden
      </p>
    </section>
  );
}
