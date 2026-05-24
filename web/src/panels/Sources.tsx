import { useEffect, useMemo } from 'preact/hooks';
import { useSignal, type Signal } from '@preact/signals';
import { apiGet } from '../api/client';
import type { SourceRow, SourceChannelRow, SourcesResponse } from '../api/types';
import { rangeSignal } from '../state/range';
import { filtersSignal } from '../state/filters';
import { siteSignal, activeSiteSignal } from '../state/site';
import { DualBar } from './DualBar';
import { DualSortHeader, SortHeader } from '../components/SortHeader';
import { SourcesByChannelChart } from './SourcesByChannelChart';
import { fmtInt, fmtRpv } from '../lib/fmt';
import { rowMax } from '../lib/rows';
import './panels.css';

// Channel header rows derive their totals from the server's HLL-merged
// by_channel rollup; never sum per-referrer visitor counts client-side
// (HLL union is sub-additive when visitors overlap across referrers).
export default function Sources() {
  const data = useSignal<SourcesResponse | null>(null);
  const err = useSignal<string | null>(null);
  const expanded = useSignal<Record<string, boolean>>({});

  useEffect(() => {
    err.value = null;
    const ac = new AbortController();

    (async () => {
      try {
        const r = rangeSignal.value;
        data.value = await apiGet<SourcesResponse>(
          '/api/stats/sources',
          { from: r.from, to: r.to },
          ac.signal,
        );
      } catch (e: unknown) {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        err.value = e instanceof Error ? e.message : String(e);
      }
    })();

    return () => ac.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    siteSignal.value,
    rangeSignal.value.from,
    rangeSignal.value.to,
    filtersSignal.value.channel,
    filtersSignal.value.device,
    filtersSignal.value.country,
    filtersSignal.value.path,
    filtersSignal.value.sort,
    filtersSignal.value.dir,
  ]);

  if (err.value) {
    return (
      <section class="statnive-section">
        <h2 class="statnive-h2">Sources</h2>
        <p class="statnive-error">could not load; see logs</p>
      </section>
    );
  }

  const resp = data.value;
  if (!resp) {
    return (
      <section class="statnive-section">
        <h2 class="statnive-h2">Sources</h2>
        <p class="statnive-loading">loading…</p>
      </section>
    );
  }

  if (resp.rows.length === 0 && resp.by_channel.length === 0) {
    return (
      <section class="statnive-section">
        <h2 class="statnive-h2">Sources</h2>
        <p class="statnive-empty">No source data for this range / filter.</p>
      </section>
    );
  }

  const currency = activeSiteSignal.value?.currency ?? 'EUR';

  return (
    <section class="statnive-section" data-testid="panel-sources">
      <h2 class="statnive-h2">Sources</h2>
      <SourcesByChannelChart by_channel={resp.by_channel} currency={currency} />
      <SourcesTable
        rows={resp.rows}
        byChannel={resp.by_channel}
        currency={currency}
        expanded={expanded}
      />
    </section>
  );
}

interface SourcesTableProps {
  rows: SourceRow[];
  byChannel: SourceChannelRow[];
  currency: string;
  expanded: Signal<Record<string, boolean>>;
}

function SourcesTable({ rows, byChannel, currency, expanded }: SourcesTableProps) {
  // by_channel drives both the order of headers (revenue-DESC from the
  // server) and the totals shown in each header row.
  const groups = useMemo(() => {
    const map = new Map<string, SourceRow[]>();
    for (const r of rows) {
      const arr = map.get(r.channel);
      if (arr) arr.push(r);
      else map.set(r.channel, [r]);
    }
    return map;
  }, [rows]);

  const channelMaxes = useMemo(
    () => ({
      visitors: rowMax(byChannel, (r) => r.visitors),
      revenue: rowMax(byChannel, (r) => r.revenue),
    }),
    [byChannel],
  );

  const referrerMaxes = useMemo(
    () => ({
      visitors: rowMax(rows, (r) => r.visitors),
      revenue: rowMax(rows, (r) => r.revenue),
    }),
    [rows],
  );

  function toggle(channel: string) {
    expanded.value = { ...expanded.value, [channel]: !expanded.value[channel] };
  }

  return (
    <table class="statnive-table statnive-sources-table">
      <thead>
        <tr>
          <th scope="col" class="statnive-channel-chevron-col" aria-hidden="true" />
          <SortHeader label="Channel" column="channel" />
          <SortHeader label="Views" column="views" />
          <SortHeader label="Goals" column="goals" />
          <SortHeader label="RPV" column="rpv" />
          <DualSortHeader />
        </tr>
      </thead>
      <tbody>
        {byChannel.map((ch, i) => {
          const channelRows = groups.get(ch.channel) ?? [];
          const hasRows = channelRows.length > 0;
          const isOpen = !!expanded.value[ch.channel];
          const detailId = `statnive-channel-detail-${i}`;
          return (
            <ChannelGroup
              key={ch.channel}
              channel={ch}
              channelRows={channelRows}
              isOpen={isOpen}
              hasRows={hasRows}
              detailId={detailId}
              channelMaxes={channelMaxes}
              referrerMaxes={referrerMaxes}
              currency={currency}
              onToggle={() => toggle(ch.channel)}
            />
          );
        })}
      </tbody>
    </table>
  );
}

interface ChannelGroupProps {
  channel: SourceChannelRow;
  channelRows: SourceRow[];
  isOpen: boolean;
  hasRows: boolean;
  detailId: string;
  channelMaxes: { visitors: number; revenue: number };
  referrerMaxes: { visitors: number; revenue: number };
  currency: string;
  onToggle: () => void;
}

function ChannelGroup({
  channel,
  channelRows,
  isOpen,
  hasRows,
  detailId,
  channelMaxes,
  referrerMaxes,
  currency,
  onToggle,
}: ChannelGroupProps) {
  function onKeyDown(e: KeyboardEvent) {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      onToggle();
    }
  }

  return (
    <>
      <tr
        class="statnive-channel-row"
        aria-expanded={hasRows ? isOpen : undefined}
        aria-controls={hasRows ? detailId : undefined}
        aria-disabled={hasRows ? undefined : true}
        tabIndex={hasRows ? 0 : -1}
        onClick={hasRows ? onToggle : undefined}
        onKeyDown={hasRows ? onKeyDown : undefined}
        data-channel={channel.channel}
      >
        <td class="statnive-channel-chevron-col">
          {hasRows ? (
            <span class="statnive-chevron" aria-hidden="true" />
          ) : (
            <span aria-hidden="true">·</span>
          )}
        </td>
        <td>
          <span
            class="statnive-channel-chip statnive-channel-chip-lg"
            data-channel={channel.channel}
          >
            {channel.channel || '·'}
          </span>
        </td>
        <td>{fmtInt(channel.views)}</td>
        <td>{fmtInt(channel.goals)}</td>
        <td>{fmtRpv(channel.rpv, currency)}</td>
        <td>
          <DualBar
            visitors={channel.visitors}
            revenue={channel.revenue}
            maxVisitors={channelMaxes.visitors}
            maxRevenue={channelMaxes.revenue}
            currency={currency}
          />
        </td>
      </tr>
      {channelRows.map((r) => (
        <tr
          key={r.referrer_name + '|' + r.channel}
          class="statnive-channel-detail"
          id={detailId}
          hidden={!isOpen}
        >
          <td class="statnive-channel-chevron-col" aria-hidden="true" />
          <td class="statnive-referrer-cell">{r.referrer_name || '(direct)'}</td>
          <td>{fmtInt(r.views)}</td>
          <td>{fmtInt(r.goals)}</td>
          <td>{fmtRpv(r.rpv, currency)}</td>
          <td>
            <DualBar
              visitors={r.visitors}
              revenue={r.revenue}
              maxVisitors={referrerMaxes.visitors}
              maxRevenue={referrerMaxes.revenue}
              currency={currency}
            />
          </td>
        </tr>
      ))}
    </>
  );
}
