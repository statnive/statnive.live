---
title: "@preact/signals Documentation"
library_id: /preactjs/signals
type: context7-reference
created: 2026-04-17
updated: 2026-04-17
context7_mode: code
topic: signals-hooks-batching
tags: [context7, preact, signals, reactivity, state]
source: Context7 MCP
cache_ttl: 7 days
---

# @preact/signals — Hooks and optimized rendering (confirmed)

## Core hooks

```jsx
import { useSignal, useComputed, useSignalEffect } from "@preact/signals";

function Overview() {
  const visitors = useSignal(0);
  const revenue  = useSignal(0);
  const rpv      = useComputed(() => visitors.value === 0 ? 0 : revenue.value / visitors.value);

  useSignalEffect(() => {
    console.log(`RPV updated: ${rpv.value}`);
  });

  return (
    <div>
      <h2>Visitors: {visitors}</h2>          {/* optimized — bound to DOM text node */}
      <h2>Revenue:  {revenue}</h2>            {/* optimized */}
      <h2>RPV:      {rpv}</h2>
    </div>
  );
}
```

## Optimized rendering — **critical for dashboard performance**

| Code | Effect |
|------|--------|
| `<p>{count.value}</p>` | **Unoptimized** — triggers parent re-render |
| `<p>{count}</p>`       | **Optimized** — updates DOM text node directly, no vdom diff |

**Action for statnive-live:** In Overview / Sources panels that update every 60s (cache TTL), always pass the signal directly, not `.value`, to avoid re-rendering table rows.

## Batching multiple updates

```js
import { batch } from "@preact/signals-core";

batch(() => {
  visitors.value = newVisitors;
  revenue.value  = newRevenue;
  // Only ONE effect/render triggered, not two
});
```

Use in `fetch().then()` handlers where multiple signals update from one response.

## No API deltas vs 2026-04-17 snapshot.
