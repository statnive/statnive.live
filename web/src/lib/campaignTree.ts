import type { CampaignRow } from '../api/types';

// CampaignTree turns the flat /api/stats/campaigns row set (one row per
// utm tuple) into a 4-level Campaign → Source → Medium → Content tree
// that the panel renders with lazy expand. utm_term is hung off content
// rows as a secondary chip rather than its own level — for non-paid
// search traffic it's empty, and a 5-deep indent reads as noise.
//
// The HLL caveat: visitor counts on parent nodes are `sum(children)`,
// not a true HLL merge. A visitor who used the same campaign through
// two different (source, medium, content) combos counts twice at the
// campaign level. In tagged traffic this overlap is small (a tracker
// tag pins the visitor to one combo), so summing is an acceptable
// approximation. The "visitors" column on parent rows is labelled with
// a `title` tooltip noting the aggregation.

export type CampaignTreeLevel = 'campaign' | 'source' | 'medium' | 'content';

export interface CampaignTreeNode {
  level: CampaignTreeLevel;
  label: string;
  pathKey: string;
  terms: string[];
  views: number;
  visitors: number;
  goals: number;
  revenue: number;
  rpv: number;
  children: CampaignTreeNode[];
}

const NONE_LABEL = '(none)';

// Path separator — NUL never appears in URL-decoded UTM values, so
// concatenating with it cannot collide. (Without a separator,
// ("abc","def") and ("ab","cdef") would share the same pathKey.)
const PATH_SEP = '\u0000';

function label(value: string): string {
  return value === '' ? NONE_LABEL : value;
}

function rpvOf(revenue: number, visitors: number): number {
  return visitors === 0 ? 0 : revenue / visitors;
}

function emptyNode(
  level: CampaignTreeLevel,
  rawValue: string,
  parentPath: string,
): CampaignTreeNode {
  return {
    level,
    label: label(rawValue),
    pathKey: parentPath === '' ? rawValue : `${parentPath}${PATH_SEP}${rawValue}`,
    terms: [],
    views: 0,
    visitors: 0,
    goals: 0,
    revenue: 0,
    rpv: 0,
    children: [],
  };
}

function accumulate(node: CampaignTreeNode, row: CampaignRow): void {
  node.views += row.views;
  node.visitors += row.visitors;
  node.goals += row.goals;
  node.revenue += row.revenue;
}

// childBuckets keys parent → child-lookup map. Side-tabled in a WeakMap
// so the internal lookup never leaks onto the public CampaignTreeNode
// shape and GC reclaims it as soon as buildCampaignTree returns.
type BucketTable = WeakMap<CampaignTreeNode, Map<string, CampaignTreeNode>>;

function findOrCreateChild(
  buckets: BucketTable,
  parent: CampaignTreeNode,
  level: CampaignTreeLevel,
  rawValue: string,
): CampaignTreeNode {
  let inner = buckets.get(parent);
  if (!inner) {
    inner = new Map<string, CampaignTreeNode>();
    buckets.set(parent, inner);
  }
  const existing = inner.get(rawValue);
  if (existing) return existing;
  const node = emptyNode(level, rawValue, parent.pathKey);
  inner.set(rawValue, node);
  parent.children.push(node);
  return node;
}

function dedupeTerms(node: CampaignTreeNode): void {
  if (node.terms.length === 0) return;
  const seen = new Set<string>();
  const out: string[] = [];
  for (const term of node.terms) {
    if (term === '' || seen.has(term)) continue;
    seen.add(term);
    out.push(term);
  }
  node.terms = out;
}

function finalize(node: CampaignTreeNode): void {
  node.rpv = rpvOf(node.revenue, node.visitors);
  for (const child of node.children) finalize(child);
  dedupeTerms(node);
}

export function buildCampaignTree(rows: CampaignRow[]): CampaignTreeNode[] {
  const topBucket = new Map<string, CampaignTreeNode>();
  const top: CampaignTreeNode[] = [];
  const buckets: BucketTable = new WeakMap();

  for (const row of rows) {
    let campaign = topBucket.get(row.utm_campaign);
    if (!campaign) {
      campaign = emptyNode('campaign', row.utm_campaign, '');
      topBucket.set(row.utm_campaign, campaign);
      top.push(campaign);
    }
    accumulate(campaign, row);

    const source = findOrCreateChild(buckets, campaign, 'source', row.utm_source);
    accumulate(source, row);

    const medium = findOrCreateChild(buckets, source, 'medium', row.utm_medium);
    accumulate(medium, row);

    const content = findOrCreateChild(buckets, medium, 'content', row.utm_content);
    accumulate(content, row);
    content.terms.push(row.utm_term);
  }

  for (const node of top) finalize(node);

  top.sort(byRevenueDesc);
  for (const node of top) sortDeep(node);

  return top;
}

function byRevenueDesc(a: CampaignTreeNode, b: CampaignTreeNode): number {
  if (b.revenue !== a.revenue) return b.revenue - a.revenue;
  return b.views - a.views;
}

function sortDeep(node: CampaignTreeNode): void {
  node.children.sort(byRevenueDesc);
  for (const child of node.children) sortDeep(child);
}

// treeNodes returns every node in the tree (parents + leaves) so the
// table's DualBar scaling stays consistent whether a parent row is
// collapsed or expanded — without this, expanding a tall campaign would
// re-shrink the other rows as new children entered the visible set.
export function treeNodes(tree: CampaignTreeNode[]): CampaignTreeNode[] {
  const out: CampaignTreeNode[] = [];
  const walk = (node: CampaignTreeNode) => {
    out.push(node);
    for (const child of node.children) walk(child);
  };
  for (const node of tree) walk(node);
  return out;
}

// allPathKeys returns the pathKey set of every node currently in the
// tree. Used by Campaigns.tsx to garbage-collect the `expanded` set
// when the upstream data changes (range / filter / site) so stale keys
// don't accumulate across a long session.
export function allPathKeys(tree: CampaignTreeNode[]): Set<string> {
  const out = new Set<string>();
  const walk = (node: CampaignTreeNode) => {
    out.add(node.pathKey);
    for (const child of node.children) walk(child);
  };
  for (const node of tree) walk(node);
  return out;
}

// topCampaignAggregates collapses the tree to one entry per campaign
// for the chart strip. Output is sorted by revenue desc, then views.
export interface CampaignAggregate {
  utm_campaign: string;
  views: number;
  visitors: number;
  goals: number;
  revenue: number;
  rpv: number;
}

export function topCampaignAggregates(
  tree: CampaignTreeNode[],
  limit: number,
): CampaignAggregate[] {
  const head = tree.slice(0, limit);
  return head.map((node) => ({
    utm_campaign: node.label,
    views: node.views,
    visitors: node.visitors,
    goals: node.goals,
    revenue: node.revenue,
    rpv: node.rpv,
  }));
}

// pieSlices turns a top-N aggregate set into donut-ready slices. Anything
// outside the top `limit` collapses into an "Other" slice so the donut
// stays readable. Percentages sum to 100 (rounding allocated to the
// largest slice so the visual chart fills the circle).
export interface PieSlice {
  label: string;
  value: number;
  percent: number;
  color: string;
}

const PIE_PALETTE = [
  'var(--chart-revenue)',
  'var(--chart-visitors)',
  'var(--chart-ochre)',
  'var(--chart-plum)',
  'var(--chart-rust)',
  'var(--ch-referral)',
];
const PIE_OTHER_COLOR = 'var(--rule-soft)';

export function pieSlices(
  tree: CampaignTreeNode[],
  pick: (n: CampaignTreeNode) => number,
  limit: number,
): PieSlice[] {
  const sorted = [...tree].sort((a, b) => pick(b) - pick(a));
  const head = sorted.slice(0, limit);
  const tail = sorted.slice(limit);
  const tailTotal = tail.reduce((sum, node) => sum + pick(node), 0);
  const total =
    head.reduce((sum, node) => sum + pick(node), 0) + tailTotal;

  if (total <= 0) return [];

  const slices: PieSlice[] = head.map((node, idx) => ({
    label: node.label,
    value: pick(node),
    percent: (pick(node) / total) * 100,
    color: PIE_PALETTE[idx % PIE_PALETTE.length],
  }));

  if (tailTotal > 0) {
    slices.push({
      label: 'Other',
      value: tailTotal,
      percent: (tailTotal / total) * 100,
      color: PIE_OTHER_COLOR,
    });
  }

  return slices;
}
