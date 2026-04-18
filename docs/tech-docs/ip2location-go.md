---
title: ip2location-go/v9 Documentation
library_id: /ip2location/ip2location-go
type: context7-reference
created: 2026-04-17
updated: 2026-04-17
context7_mode: code
topic: db23-lookup
tags: [context7, ip2location, geoip, enrichment]
source: Context7 MCP
cache_ttl: 7 days
---

# ip2location-go/v9 — GeoIP lookup (confirmed)

## Basic usage for statnive-live `internal/enrich/geoip.go`

```go
import "github.com/ip2location/ip2location-go/v9"

db, err := ip2location.OpenDB("/var/lib/statnive-live/IP2LOCATION-DB23.BIN")
if err != nil {
    return nil, fmt.Errorf("geoip open: %w", err)
}
defer db.Close()

result, err := db.Get_all("8.8.8.8")     // or IPv6
// result.Country_short, result.Region, result.City
// result.Isp, result.Asn, result.As, result.Asdomain
```

## DB23 fields relevant to statnive-live

- `Country_short` (2-char ISO) → `country_code` column
- `Region` → `province` column
- `City` → `city` column
- `Isp` → `isp` column (enrichment for Geo panel)
- `Asn`, `As`, `Asdomain` → ASN-based bot filter
- `Latitude`, `Longitude` → map visualization

## Granular methods (avoid Get_all when you only need 1-2 fields)

```go
cityResult, err := db.Get_city("1.2.3.4")   // City only — lower allocation
```

## Thread safety

`*ip2location.DB` is **safe for concurrent reads**. Open once at startup, share across all 6 enrichment workers.

## License: MIT (confirmed via PLAN.md)

## No API deltas vs 2026-04-17 snapshot.
