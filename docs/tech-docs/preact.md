---
title: Preact Documentation
library_id: /preactjs/preact-www
type: context7-reference
created: 2026-04-17
updated: 2026-04-17
context7_mode: code
topic: hooks-refs-signals
tags: [context7, preact, hooks, signals, dashboard-spa]
source: Context7 MCP
cache_ttl: 7 days
---

# Preact — Hooks + refs + signals integration (confirmed)

## useRef + useEffect (for chart container mounting)

```jsx
import { useRef, useEffect } from 'preact/hooks';

export function Chart({ data }) {
  const containerRef = useRef();
  const chartRef = useRef();

  useEffect(() => {
    chartRef.current = new uPlot(opts, data, containerRef.current);
    return () => chartRef.current.destroy();
  }, []);

  useEffect(() => {
    chartRef.current?.setData(data);
  }, [data]);

  return <div ref={containerRef} />;
}
```

## useState + useEffect (for async data loading before signals migration)

```jsx
import { useState, useEffect } from 'preact/hooks';

function Sources() {
  const [rows, setRows] = useState([]);
  useEffect(() => {
    fetch('/api/stats/sources').then(r => r.json()).then(setRows);
  }, []);
  return <Table data={rows} />;
}
```

**Prefer `@preact/signals` over `useState` in statnive-live** per Architecture Rule: "Signals auto-update JSX without re-renders — better for real-time metric displays." (See `preact-signals.md`.)

## Signals + Preact integration (the idiomatic statnive-live pattern)

```jsx
import { render } from 'preact';
import { signal } from '@preact/signals';

const count = signal(0);

function Counter() {
  return (
    <div>
      <button onClick={() => count.value++}>Increment</button>
      <input readonly value={count} />   {/* optimized: binds to DOM directly */}
    </div>
  );
}

render(<Counter />, document.getElementById('app'));
```

## License: MIT

## No API deltas vs 2026-04-17 snapshot.
