---
title: go-chi/chi v5 Documentation
library_id: /go-chi/docs
type: context7-reference
created: 2026-04-17
updated: 2026-04-17
context7_mode: code
topic: middleware-groups
tags: [context7, go-chi, router, middleware]
source: Context7 MCP
cache_ttl: 7 days
---

# go-chi/chi v5 — Router setup (confirmed)

## Recommended middleware stack for statnive-live

```go
r := chi.NewRouter()
r.Use(middleware.RequestID)
r.Use(middleware.RealIP)          // Reads X-Real-IP then X-Forwarded-For
r.Use(middleware.Logger)
r.Use(middleware.Recoverer)
r.Use(middleware.CleanPath)
r.Use(middleware.Timeout(60 * time.Second))
r.Use(middleware.Compress(5, "application/json", "text/html"))
```

## RealIP middleware — **SECURITY NOTE**

> "You should only use this middleware if you can trust the headers passed to you (e.g., because you have placed a reverse proxy like HAProxy or nginx in front of chi). If your reverse proxies are configured to pass along arbitrary header values from the client, or if you use this middleware without a reverse proxy, malicious clients will be able to cause harm."

**Action for statnive-live:** Only register `middleware.RealIP` if deployed behind Caddy/nginx. Direct-exposure mode must fall back to `r.RemoteAddr`.

## Mounting sub-routers

```go
// Ingest (public, rate-limited)
ingestRouter := chi.NewRouter()
ingestRouter.Use(httprate.LimitByRealIP(100, time.Second))
ingestRouter.Post("/event", handlers.IngestEvent)
r.Mount("/api", ingestRouter)

// Dashboard (protected)
dashRouter := chi.NewRouter()
dashRouter.Use(auth.RequireSession)
dashRouter.Get("/stats/overview", handlers.Overview)
r.Mount("/dash", dashRouter)
```

## No API deltas vs 2026-04-17 snapshot.
