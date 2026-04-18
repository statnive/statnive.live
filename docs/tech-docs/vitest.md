---
title: Vitest Documentation
library_id: /vitest-dev/vitest
type: context7-reference
created: 2026-04-17
updated: 2026-04-17
context7_mode: code
topic: fake-timers-preact
tags: [context7, vitest, testing, preact, fake-timers]
source: Context7 MCP
cache_ttl: 7 days
---

# Vitest — Unit + component testing (confirmed v4)

## For `web/src/**/*.test.tsx` (Preact components)

```ts
// web/vitest.config.ts
import { defineConfig } from 'vitest/config'

export default defineConfig({
  test: {
    environment: 'happy-dom',
    setupFiles: ['./src/test-setup.ts'],
    globals: true,
  },
})
```

## Fake timers (for dashboard refresh intervals, salt rotation)

```ts
import { beforeEach, afterEach, describe, it, expect, vi } from 'vitest'

beforeEach(() => { vi.useFakeTimers() })
afterEach(()  => { vi.useRealTimers() })

it('refreshes Overview every 60s', () => {
  const fetchMock = vi.fn()
  setupOverview(fetchMock)

  expect(fetchMock).toHaveBeenCalledTimes(1)   // initial load

  vi.advanceTimersByTime(60_000)
  expect(fetchMock).toHaveBeenCalledTimes(2)   // after 60s

  vi.advanceTimersByTime(60_000)
  expect(fetchMock).toHaveBeenCalledTimes(3)
})
```

## System time mocking (IRST midnight salt rotation tests)

```ts
const irstMidnight = new Date('2026-04-17T20:30:00Z')  // 00:00 IRST
vi.setSystemTime(irstMidnight)

rotateSalt()
expect(currentSalt()).not.toBe(previousSalt)

vi.useRealTimers()
```

## `vi.useFakeTimers({ toFake: ['nextTick', 'queueMicrotask'] })`

By default, `process.nextTick` and `queueMicrotask` are NOT mocked. Opt-in if your code relies on them (likely not for dashboard UI).

## Version note

`/vitest-dev/vitest` indexed at **v3.2.4 and v4.0.7**. Vitest 4 is GA.

## License: MIT

## No API deltas vs 2026-04-17 snapshot (Vitest 4 GA was known).
