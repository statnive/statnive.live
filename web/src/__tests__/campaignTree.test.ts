import { describe, expect, it } from 'vitest';
import {
  allPathKeys,
  buildCampaignTree,
  pieSlices,
  topCampaignAggregates,
  treeNodes,
} from '../lib/campaignTree';
import type { CampaignRow } from '../api/types';

function row(partial: Partial<CampaignRow>): CampaignRow {
  return {
    utm_campaign: '',
    utm_source: '',
    utm_medium: '',
    utm_content: '',
    utm_term: '',
    views: 0,
    visitors: 0,
    goals: 0,
    revenue: 0,
    rpv: 0,
    ...partial,
  };
}

describe('buildCampaignTree', () => {
  it('returns an empty array when input is empty', () => {
    expect(buildCampaignTree([])).toEqual([]);
  });

  it('groups by campaign, then source, then medium, then content', () => {
    const rows: CampaignRow[] = [
      row({
        utm_campaign: 'launch',
        utm_source: 'google',
        utm_medium: 'cpc',
        utm_content: 'banner-a',
        views: 10,
        visitors: 5,
        revenue: 100,
      }),
      row({
        utm_campaign: 'launch',
        utm_source: 'google',
        utm_medium: 'cpc',
        utm_content: 'banner-b',
        views: 4,
        visitors: 3,
        revenue: 40,
      }),
      row({
        utm_campaign: 'launch',
        utm_source: 'facebook',
        utm_medium: 'social',
        utm_content: 'feed-1',
        views: 7,
        visitors: 6,
        revenue: 20,
      }),
    ];

    const tree = buildCampaignTree(rows);
    expect(tree).toHaveLength(1);

    const launch = tree[0];
    expect(launch.label).toBe('launch');
    expect(launch.level).toBe('campaign');
    expect(launch.views).toBe(21);
    expect(launch.visitors).toBe(14);
    expect(launch.revenue).toBe(160);
    expect(launch.children).toHaveLength(2);

    const google = launch.children.find((c) => c.label === 'google');
    expect(google?.level).toBe('source');
    expect(google?.children).toHaveLength(1);
    const cpc = google?.children[0];
    expect(cpc?.label).toBe('cpc');
    expect(cpc?.children).toHaveLength(2);
    const banners = cpc?.children.map((c) => c.label).sort();
    expect(banners).toEqual(['banner-a', 'banner-b']);
  });

  it('renders empty UTM values as "(none)" and dedupes term chips', () => {
    const tree = buildCampaignTree([
      row({
        utm_campaign: 'spring',
        utm_source: '',
        utm_medium: '',
        utm_content: '',
        utm_term: 'shoes',
        visitors: 1,
      }),
      row({
        utm_campaign: 'spring',
        utm_source: '',
        utm_medium: '',
        utm_content: '',
        utm_term: 'shoes',
        visitors: 1,
      }),
      row({
        utm_campaign: 'spring',
        utm_source: '',
        utm_medium: '',
        utm_content: '',
        utm_term: 'boots',
        visitors: 1,
      }),
    ]);

    const content = tree[0].children[0].children[0].children[0];
    expect(content.label).toBe('(none)');
    expect(content.terms).toEqual(['shoes', 'boots']);
  });

  it('computes RPV per node and sorts children by revenue desc', () => {
    const tree = buildCampaignTree([
      row({
        utm_campaign: 'a',
        utm_source: 's1',
        utm_medium: 'm1',
        utm_content: 'c1',
        revenue: 200,
        visitors: 10,
      }),
      row({
        utm_campaign: 'a',
        utm_source: 's2',
        utm_medium: 'm1',
        utm_content: 'c1',
        revenue: 50,
        visitors: 10,
      }),
    ]);

    expect(tree[0].rpv).toBeCloseTo(12.5);
    expect(tree[0].children.map((c) => c.label)).toEqual(['s1', 's2']);
  });
});

describe('pathKey collision prevention', () => {
  it('separates parent and child by a delimiter so ("ab","cdef") and ("abc","def") never share a key', () => {
    const tree = buildCampaignTree([
      row({
        utm_campaign: 'ab',
        utm_source: 'cdef',
        utm_medium: '',
        utm_content: '',
      }),
      row({
        utm_campaign: 'abc',
        utm_source: 'def',
        utm_medium: '',
        utm_content: '',
      }),
    ]);

    expect(tree).toHaveLength(2);
    const sourceA = tree.find((c) => c.label === 'ab')!.children[0];
    const sourceB = tree.find((c) => c.label === 'abc')!.children[0];
    expect(sourceA.pathKey).not.toBe(sourceB.pathKey);
  });
});

describe('treeNodes + allPathKeys', () => {
  it('treeNodes returns every node (parents + children) in DFS order', () => {
    const tree = buildCampaignTree([
      row({
        utm_campaign: 'c',
        utm_source: 's',
        utm_medium: 'm',
        utm_content: 'co',
        visitors: 1,
      }),
    ]);
    const nodes = treeNodes(tree);
    expect(nodes.map((n) => n.level)).toEqual([
      'campaign',
      'source',
      'medium',
      'content',
    ]);
  });

  it('allPathKeys returns the full set of pathKeys for GC of expanded state', () => {
    const tree = buildCampaignTree([
      row({ utm_campaign: 'x', utm_source: 's1' }),
      row({ utm_campaign: 'x', utm_source: 's2' }),
    ]);
    const keys = allPathKeys(tree);
    expect(keys.size).toBeGreaterThan(1);
    expect(keys.has(tree[0].pathKey)).toBe(true);
    expect(keys.has(tree[0].children[0].pathKey)).toBe(true);
  });
});

describe('topCampaignAggregates', () => {
  it('caps to limit and preserves revenue-desc order from the tree', () => {
    const tree = buildCampaignTree(
      ['c1', 'c2', 'c3', 'c4'].map((name, i) =>
        row({ utm_campaign: name, revenue: (4 - i) * 10, visitors: 1 }),
      ),
    );
    const agg = topCampaignAggregates(tree, 2);
    expect(agg.map((a) => a.utm_campaign)).toEqual(['c1', 'c2']);
  });
});

describe('pieSlices', () => {
  it('collapses tail into "Other" and percentages sum to ~100', () => {
    const tree = buildCampaignTree(
      Array.from({ length: 8 }, (_, i) =>
        row({ utm_campaign: `c${i}`, visitors: 10 - i }),
      ),
    );
    const slices = pieSlices(tree, (n) => n.visitors, 3);
    expect(slices).toHaveLength(4);
    expect(slices[slices.length - 1].label).toBe('Other');
    const sum = slices.reduce((s, p) => s + p.percent, 0);
    expect(sum).toBeCloseTo(100, 5);
  });

  it('returns [] when total is zero', () => {
    const tree = buildCampaignTree([
      row({ utm_campaign: 'empty', visitors: 0 }),
    ]);
    expect(pieSlices(tree, (n) => n.visitors, 5)).toEqual([]);
  });
});
