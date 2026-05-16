import { useEffect, useMemo } from 'preact/hooks';
import { useSignal } from '@preact/signals';
import { apiGet } from '../api/client';
import type { CampaignRow } from '../api/types';
import { rangeSignal } from '../state/range';
import { filtersSignal } from '../state/filters';
import { siteSignal, activeSiteSignal } from '../state/site';
import {
  allPathKeys,
  buildCampaignTree,
  treeNodes,
  type CampaignTreeNode,
} from '../lib/campaignTree';
import { DualBar } from './DualBar';
import { CampaignCharts } from './CampaignCharts';
import { fmtInt, fmtRpv } from '../lib/fmt';
import { rowMax } from '../lib/rows';
import './panels.css';
import './CampaignTree.css';

const VISITORS_TOOLTIP =
  'Sum across child UTM combos; approximate when the same visitor used multiple combos.';

interface RenderCtx {
  expanded: Set<string>;
  toggle: (key: string) => void;
  maxVisitors: number;
  maxRevenue: number;
  currency: string;
}

export default function Campaigns() {
  const data = useSignal<CampaignRow[] | null>(null);
  const err = useSignal<string | null>(null);
  const expanded = useSignal<Set<string>>(new Set<string>());

  useEffect(() => {
    err.value = null;
    const ac = new AbortController();

    (async () => {
      try {
        const r = rangeSignal.value;
        data.value = await apiGet<CampaignRow[]>(
          '/api/stats/campaigns',
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
  ]);

  const rows = data.value;
  const tree = useMemo(() => (rows ? buildCampaignTree(rows) : []), [rows]);
  const currency = activeSiteSignal.value?.currency ?? 'EUR';
  const allNodes = useMemo(() => treeNodes(tree), [tree]);
  const maxVisitors = rowMax(allNodes, (n) => n.visitors);
  const maxRevenue = rowMax(allNodes, (n) => n.revenue);

  // Garbage-collect stale expanded keys when the upstream data changes
  // (range / filter / site). Without this the Set grows unbounded across
  // a long session as the user expands campaigns that no longer exist
  // in the latest tree.
  useMemo(() => {
    if (!rows) return;
    const live = allPathKeys(tree);
    let stale = false;
    for (const key of expanded.value) {
      if (!live.has(key)) {
        stale = true;
        break;
      }
    }
    if (!stale) return;
    const next = new Set<string>();
    for (const key of expanded.value) if (live.has(key)) next.add(key);
    expanded.value = next;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tree]);

  if (err.value) {
    return (
      <section class="statnive-section">
        <h2 class="statnive-h2">Campaigns</h2>
        <p class="statnive-error">could not load; see logs</p>
      </section>
    );
  }

  if (!rows) {
    return (
      <section class="statnive-section">
        <h2 class="statnive-h2">Campaigns</h2>
        <p class="statnive-loading">loading…</p>
      </section>
    );
  }

  if (rows.length === 0) {
    return (
      <section class="statnive-section">
        <h2 class="statnive-h2">Campaigns</h2>
        <p class="statnive-empty">No utm_campaign data for this range / filter.</p>
      </section>
    );
  }

  const toggle = (key: string) => {
    const next = new Set(expanded.value);
    if (next.has(key)) next.delete(key);
    else next.add(key);
    expanded.value = next;
  };

  const ctx: RenderCtx = {
    expanded: expanded.value,
    toggle,
    maxVisitors,
    maxRevenue,
    currency,
  };

  return (
    <section class="statnive-section" data-testid="panel-campaigns">
      <h2 class="statnive-h2">Campaigns</h2>

      <CampaignCharts tree={tree} rows={rows} currency={currency} />

      <table class="statnive-table statnive-tree-table">
        <thead>
          <tr>
            <th scope="col">Campaign · Source · Medium · Content</th>
            <th scope="col">Views</th>
            <th scope="col" title={VISITORS_TOOLTIP}>Visitors *</th>
            <th scope="col">Goals</th>
            <th scope="col">RPV</th>
            <th scope="col">Visitors / Revenue</th>
          </tr>
        </thead>
        <tbody>{tree.flatMap((node) => renderNode(node, 0, ctx))}</tbody>
      </table>
      <p class="statnive-tree-footnote">
        * Visitors on parent rows are summed across child UTM combos; a
        visitor who used two combos is counted twice. Leaf (Content)
        rows are HLL-exact per combo.
      </p>
    </section>
  );
}

function renderNode(
  node: CampaignTreeNode,
  depth: number,
  ctx: RenderCtx,
): preact.JSX.Element[] {
  const hasChildren = node.children.length > 0;
  const isOpen = ctx.expanded.has(node.pathKey);
  const rows: preact.JSX.Element[] = [
    <tr
      key={node.pathKey}
      data-level={node.level}
      class={`statnive-tree-row is-${node.level}`}
    >
      <td>
        <span
          class="statnive-tree-indent"
          style={{ paddingLeft: depth * 18 + 'px' }}
        >
          {hasChildren ? (
            <button
              type="button"
              class="statnive-tree-chevron"
              aria-expanded={isOpen}
              aria-label={`${isOpen ? 'Collapse' : 'Expand'} ${node.label}`}
              onClick={() => ctx.toggle(node.pathKey)}
            >
              {isOpen ? '▾' : '▸'}
            </button>
          ) : (
            <span class="statnive-tree-chevron-spacer" />
          )}
          <span class="statnive-tree-label">{node.label}</span>
          {node.level === 'content' && node.terms.length > 0 ? (
            <span class="statnive-tree-terms" title="utm_term values">
              {node.terms.map((t) => (
                <span key={t} class="statnive-tree-term-chip">
                  {t}
                </span>
              ))}
            </span>
          ) : null}
        </span>
      </td>
      <td>{fmtInt(node.views)}</td>
      <td title={depth > 0 || hasChildren ? VISITORS_TOOLTIP : undefined}>
        {fmtInt(node.visitors)}
      </td>
      <td>{fmtInt(node.goals)}</td>
      <td>{fmtRpv(node.rpv, ctx.currency)}</td>
      <td>
        <DualBar
          visitors={node.visitors}
          revenue={node.revenue}
          maxVisitors={ctx.maxVisitors}
          maxRevenue={ctx.maxRevenue}
          currency={ctx.currency}
        />
      </td>
    </tr>,
  ];

  if (hasChildren && isOpen) {
    for (const child of node.children) {
      rows.push(...renderNode(child, depth + 1, ctx));
    }
  }

  return rows;
}
