---
title: bits-and-blooms/bloom/v3 Documentation
library_id: /bits-and-blooms/bloom
type: context7-reference
created: 2026-04-17
updated: 2026-04-17
context7_mode: code
topic: bloom-filter-new-visitor
tags: [context7, bloom, filter, new-visitor, enrichment]
source: Context7 MCP
cache_ttl: 7 days
---

# bits-and-blooms/bloom/v3 — Bloom filter (confirmed)

## Usage for `internal/enrich/newvisitor.go` (PLAN.md: 18MB, 10M visitors)

```go
import "github.com/bits-and-blooms/bloom/v3"

// 10M expected elements, 1% false positive — sizing check:
// NewWithEstimates(10_000_000, 0.01) → ~11.5 MB (close to PLAN's 18 MB budget at 0.001 FPR)
filter := bloom.NewWithEstimates(10_000_000, 0.001)  // 0.1% FPR → ~18MB

// In enrichment worker
visitorHash := blake3.Sum128([]byte(salt + ip + ua))
if filter.Test(visitorHash[:]) {
    enriched.IsNewVisitor = false          // probably seen before
} else {
    filter.Add(visitorHash[:])
    enriched.IsNewVisitor = true           // definitely new
}
```

## Cardinality estimation (useful for dashboard metrics)

```go
count := filter.ApproximatedSize()   // estimate of distinct elements added
```

## Daily reset pattern (PLAN.md: salt rotation at IRST midnight)

At salt rotation, create a fresh filter:

```go
// Replace filter atomically (under mutex)
newFilter := bloom.NewWithEstimates(10_000_000, 0.001)
mu.Lock()
filter = newFilter
mu.Unlock()
```

## License: BSD-2 (confirmed via PLAN.md)

## No API deltas vs 2026-04-17 snapshot.
