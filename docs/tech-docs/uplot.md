---
title: uPlot Documentation
library_id: /leeoniya/uplot
type: context7-reference
created: 2026-04-17
updated: 2026-04-17
context7_mode: code
topic: time-series-streaming
tags: [context7, uplot, charts, time-series, preact]
source: Context7 MCP
cache_ttl: 7 days
---

# uPlot — Time-series charting (confirmed)

## Instance API (for Chart.tsx wrapper)

```javascript
const chart = new uPlot(opts, data, containerEl);

chart.setData(newData);                           // update data — O(1) amortized
chart.setSize({ width: 1200, height: 400 });      // on window resize
chart.setScale("x", { min: tsStart, max: tsEnd }); // programmatic zoom
chart.setSeries(idx, { show: false });            // toggle series visibility
chart.redraw();                                   // force redraw
chart.destroy();                                  // cleanup on unmount

// Utilities
chart.valToPos(50, "y");   // value → pixel
chart.posToVal(200, "x");  // pixel → value
chart.posToIdx(200);       // pixel → data index
```

## Sliding-window streaming pattern (useful for Overview real-time chart)

```javascript
setInterval(() => {
  data[0].push(now);
  data[1].push(visitors);
  if (data[0].length > 100) {
    data = data.map(arr => arr.slice(-100));
  }
  chart.setData(data);
}, 1000);
```

## Cursor sync across multiple charts (Overview → Sources → Pages hover)

```javascript
const syncGroup = uPlot.sync("dashboard");

const cursorOpts = {
  lock: true,
  focus: { prox: 16 },
  sync: {
    key: syncGroup.key,
    setSeries: true,
    match: [(a, b) => a === b, (a, b) => a === b],
  },
};
```

## Data format reminder

uPlot expects `[[x_values], [y1_values], [y2_values], …]` — columnar, NOT row-major.

## No API deltas vs 2026-04-17 snapshot.
