---
title: go-chi/httprate Documentation
library_id: /go-chi/httprate
type: context7-reference
created: 2026-04-17
updated: 2026-04-17
context7_mode: code
topic: rate-limiting-real-ip
tags: [context7, httprate, rate-limiting, real-ip, nat]
source: Context7 MCP
cache_ttl: 7 days
---

# go-chi/httprate — Rate limiting (confirmed)

## For statnive-live `/api/event` (100 req/s per IP, NAT-aware)

```go
r.Use(httprate.LimitByRealIP(100, time.Second))
```

**Header precedence:** `True-Client-IP > X-Real-IP > X-Forwarded-For > RemoteAddr`.

**On limit exceed:** 429 Too Many Requests with headers:
- `X-RateLimit-Limit: 100`
- `X-RateLimit-Remaining: 0`
- `X-RateLimit-Reset: <unix-ts>`

## Two variants

| Function | Use when |
|----------|----------|
| `LimitByIP(n, d)` | No reverse proxy (uses `r.RemoteAddr` directly) |
| `LimitByRealIP(n, d)` | Behind Caddy/nginx/Cloudflare (inspects forwarded headers) |

## Plan decision

The PLAN.md already switched away from `golang.org/x/time/rate` — confirmed the right call. **Use `LimitByRealIP` only when behind a trusted proxy**, else use `LimitByIP`.

## No API deltas vs 2026-04-17 snapshot.
