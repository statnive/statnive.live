---
title: Unindexed dependencies (Context7 miss)
type: context7-miss-fallback
created: 2026-04-17
updated: 2026-04-17
tags: [context7-miss, tidwall-wal, blake3, fallback]
source: N/A
---

# Libraries Not Indexed in Context7

These two dependencies used by statnive-live are not present in the Context7 index. Consult upstream sources directly.

## tidwall/wal (`github.com/tidwall/wal`)

- **Context7 search:** only returned tidwall's BuntDB/Rtree/Tile38/Pogocache — not `wal`.
- **Canonical docs:** https://pkg.go.dev/github.com/tidwall/wal
- **GitHub README:** https://github.com/tidwall/wal
- **Key API (stable — unchanged in 2026):**
  ```go
  log, err := wal.Open("/var/lib/statnive-live/wal", &wal.Options{
      NoSync:       false,              // fsync every write (durability over throughput)
      SegmentSize:  20 * 1024 * 1024,   // 20MB per segment
      LogFormat:    wal.Binary,
      SegmentCacheSize: 2,
      NoCopy:       false,
  })
  log.Write(idx, payload)
  log.Read(idx)
  log.TruncateFront(idx)  // after batch ACK'd by ClickHouse
  log.Sync()
  ```
- **Batch-write for 500ms/1000-row aggregation:** use `log.WriteBatch(batch)` with `wal.Batch`.

## lukechampine.com/blake3 (`lukechampine.com/blake3`)

- **Context7 search:** only returned the Rust/reference/.NET BLAKE3 implementations — the lukechampine Go port is not indexed.
- **Canonical docs:** https://pkg.go.dev/lukechampine.com/blake3
- **GitHub:** https://github.com/lukechampine/blake3
- **Key API (stable — unchanged in 2026):**
  ```go
  import "lukechampine.com/blake3"

  // 256-bit hash
  sum := blake3.Sum256(data)

  // Keyed (HMAC-style, for per-site hashing)
  keyed := blake3.New(32, siteSecret)
  keyed.Write(payload)
  out := keyed.Sum(nil)

  // XOF (arbitrary-length — used for 128-bit visitor hash)
  xof := blake3.New(16, nil)    // 16 bytes = 128 bits
  xof.Write(salt)
  xof.Write(ip)
  xof.Write(ua)
  visitorHash := xof.Sum(nil)   // 16-byte BLAKE3-128
  ```
- **Licence: MIT.** Confirmed permissive, SaaS-safe.

## Re-check on next cache TTL rollover

If either appears in Context7 later (monthly re-check), move into its own proper cache file under `tech-docs/`.
