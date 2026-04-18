---
title: hashicorp/golang-lru v2 Documentation
library_id: /hashicorp/golang-lru
type: context7-reference
created: 2026-04-17
updated: 2026-04-17
context7_mode: code
topic: expirable-lru-cache
tags: [context7, golang-lru, cache, ttl, dashboard]
source: Context7 MCP
cache_ttl: 7 days
---

# hashicorp/golang-lru v2 — LRU with TTL (confirmed)

## For `internal/cache/lru.go` — dashboard query cache (60s today, forever past)

**v2 import path is different from v1:**

```go
import "github.com/hashicorp/golang-lru/v2"
import "github.com/hashicorp/golang-lru/v2/expirable"
```

## Expirable LRU (matches PLAN.md's "60s today" pattern)

```go
// 1000 query keys, 60s TTL for today's cache
todayCache := expirable.NewLRU[string, []byte](1000, nil, 60*time.Second)

todayCache.Add("/api/stats/overview?range=today", respBytes)
if b, ok := todayCache.Get("/api/stats/overview?range=today"); ok {
    // cache hit
}
```

## Non-expirable LRU (for past-day results — never invalidated)

```go
import lru "github.com/hashicorp/golang-lru/v2"

pastCache, _ := lru.New[string, []byte](10_000)
pastCache.Add(key, value)
```

## Eviction callback signature

```go
expirable.NewLRU[K, V](size, onEvicted, ttl)
// onEvicted func(key K, value V) — nil if unused
```

## Generics (v2 only)

Type-safe with Go generics — no more `interface{}` casting. Matches statnive-live's Go 1.22+ baseline.

## License: MPL-2.0 (weak copyleft, OK for SaaS — confirmed via PLAN.md)

## No API deltas vs 2026-04-17 snapshot.
