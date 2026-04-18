---
title: clickhouse-go/v2 Documentation
library_id: /clickhouse/clickhouse-go
type: context7-reference
created: 2026-04-17
updated: 2026-04-17
context7_mode: code
topic: batch-insert-compression
tags: [context7, clickhouse-go, go, batch-insert, compression]
source: Context7 MCP
cache_ttl: 7 days
---

# clickhouse-go/v2 — Batch insert + compression (confirmed)

## Batch insert (the hot path for the ingest consumer)

```go
conn, err := clickhouse.Open(&clickhouse.Options{
    Addr: []string{"127.0.0.1:9000"},
    Auth: clickhouse.Auth{Database: "default", Username: "default"},
    Compression: &clickhouse.Compression{Method: clickhouse.CompressionLZ4},
    DialTimeout:          time.Second * 30,
    MaxOpenConns:         5,
    MaxIdleConns:         5,
    ConnMaxLifetime:      time.Hour,
    ConnOpenStrategy:     clickhouse.ConnOpenInOrder,
    BlockBufferSize:      10,
    MaxCompressionBuffer: 10240,
})

batch, err := conn.PrepareBatch(ctx, "INSERT INTO example")
for _, row := range rows {
    batch.Append(row.Col1, row.Col2, /*…33 cols…*/)
}
batch.Send()
```

**Pattern confirmed for statnive-live:** `PrepareBatch → Append → Send`. Our 33-column `events_raw` insert fits directly.

## Compression

- `CompressionLZ4` — fastest, recommended for ingest.
- `CompressionZSTD` — better ratio, use `MaxCompressionBuffer: 10485760` (10 MB).
- `MaxCompressionBuffer` controls column-block buffer size.

## Native types supported natively

`FixedString`, `Array(String)`, `DateTime64`, `Map(String, UInt8)`, `Tuple(...)`, `UUID` (via `google/uuid`).

## Connection pool knobs for ingest

- `MaxOpenConns: 5` — sufficient for single-writer batch consumer
- `ConnMaxLifetime: time.Hour` — recycle before TCP keep-alive expires
- `BlockBufferSize: 10` — rows buffered per block on receive

## No API deltas vs 2026-04-17 snapshot

All previously documented APIs still current.
