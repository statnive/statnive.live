---
title: medama-io/go-useragent Documentation
library_id: /medama-io/go-useragent
type: context7-reference
created: 2026-04-17
updated: 2026-04-17
context7_mode: code
topic: ua-parser-zero-alloc
tags: [context7, go-useragent, ua-parser, enrichment]
source: Context7 MCP
cache_ttl: 7 days
---

# medama-io/go-useragent — UA parser (confirmed zero-alloc)

## Usage for `internal/enrich/ua.go`

```go
import "github.com/medama-io/go-useragent"

// At startup (once, singleton)
var uaParser = useragent.NewParser()

// In enrichment worker
agent := uaParser.Parse(request.Header.Get("User-Agent"))

enriched.BrowserName    = string(agent.Browser())           // agents.BrowserChrome
enriched.BrowserVersion = agent.BrowserVersion()
enriched.OSName         = string(agent.OS())                 // agents.OSWindows
enriched.DeviceType     = string(agent.Device())             // agents.DeviceDesktop
enriched.IsBot          = agent.IsBot()
```

## Boolean helpers (faster than string comparison)

```go
agent.IsChrome()    agent.IsFirefox()   agent.IsSafari()
agent.IsWindows()   agent.IsLinux()     agent.IsMac()
agent.IsDesktop()   agent.IsMobile()    agent.IsTablet()   agent.IsTV()
agent.IsBot()
```

## Performance characteristics (from library README)

- **Zero allocation** on parse
- **Sub-microsecond** parse times
- Uses hybrid trie + embedded UA definitions — no external DB

Matches statnive-live's "6-worker enrichment pipeline" performance budget at 7K EPS.

## Singleton pattern mandatory

> "Create a new parser. Initialize only once during application startup."

Do NOT construct `NewParser()` per-request.

## License: MIT (confirmed via PLAN.md)

## No API deltas vs 2026-04-17 snapshot.
