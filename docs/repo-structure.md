# Repository structure (reference)

> Referenced from [PLAN.md § Repository Structure](../PLAN.md#repository-structure). Full tree of the statnive-live repo with `[shipped]` / `[planned]` / `[scaffolded]` markers. Agents reading this file: `Glob` and `Read` the actual repo when you need current state — this tree lags.

Items marked **[shipped]** exist in the working tree as of the most recent merged PR. Items marked **[planned]** are scheduled for a later phase.

```
statnive-live/                          # https://github.com/statnive/statnive.live.git
├── CLAUDE.md                           # Project rules                                                  [shipped]
├── .claude/
│   └── skills/                         # Project-local Claude skills (custom + community copies)
│       ├── tenancy-choke-point-enforcer/      # Architecture Rule 8 guardrail                          [scaffolded]
│       ├── air-gap-validator/                 # Isolation rule guardrail                               [scaffolded]
│       ├── clickhouse-rollup-correctness/     # AggregatingMergeTree / combinator discipline          [scaffolded]
│       ├── clickhouse-cluster-migration/      # {{if .Cluster}} templating guardrail                   [scaffolded]
│       ├── preact-signals-bundle-budget/      # 50KB-min / 15KB-gz + 1.2KB / 600B tracker              [scaffolded]
│       ├── blake3-hmac-identity-review/       # BLAKE3-128 + HMAC(salt) + constant-time compare       [scaffolded]
│       ├── wal-durability-review/             # fsync-before-ack + kill-9 CI gate (doc 27 §Gap 1)      [scaffolded]
│       ├── ratelimit-tuning-review/           # CGNAT-aware ASN tiering (doc 27 §Gap 2)                [scaffolded]
│       ├── gdpr-code-review/                  # HLL-anonymous + DSAR completeness (doc 27 §Gap 3)      [scaffolded]
│       ├── dsar-completeness-checker/         # sink-matrix integration test (doc 27 §Gap 3)           [scaffolded]
│       ├── iranian-dc-deploy/                 # NIN / OFAC / NSD / TLS-outside / chrony (doc 28 §Gap 2) [scaffolded]
│       ├── geoip-pipeline-review/             # atomic.Pointer swap + CC-BY-SA attribution (doc 28 §Gap 1) [scaffolded]
│       ├── clickhouse-operations-review/      # WAL-first + parts-ceiling + backup drill (doc 28 §Gap 3) [scaffolded]
│       ├── clickhouse-upgrade-playbook/       # single-node → cluster runbook (doc 28 §Gap 3, advisory) [scaffolded]
│       └── …                           # 30 doc-23 foundation skills + 17 doc-25/27 community additions [installed]
├── cmd/
│   └── statnive-live/
│       └── main.go                     # Entry point: wiring, SIGHUP fan-out, graceful shutdown         [shipped]
├── internal/
│   ├── config/
│   │   ├── secret.go                   # Master-secret loader (env → file → fail-closed)                [shipped]
│   │   └── secret_test.go                                                                                [shipped]
│   ├── audit/                          # JSONL append-only file sink (Phase 2a)                          [shipped]
│   │   ├── events.go                   # Typed EventName constants (TLS / ratelimit / ingest events)
│   │   ├── log.go                      # Logger with O_APPEND + SIGHUP-aware Reopen()
│   │   ├── log_test.go
│   │   └── audittest/
│   │       └── audittest.go            # Test-only ReadEventNames helper (substring scan)
│   ├── cert/                           # TLS lifecycle (Phase 2a)                                        [shipped]
│   │   ├── loader.go                   # atomic.Pointer hot-reload, fail-closed, keep-old-on-fail
│   │   ├── expiry.go                   # 6h ticker, <30d warn / <7d critical, crossing-dedup
│   │   ├── loader_test.go
│   │   └── expiry_test.go
│   ├── ratelimit/                      # NAT-aware go-chi/httprate wrapper (Phase 2a)                    [shipped]
│   │   ├── ratelimit.go                # Keys via ingest.ClientIP; 429 emits audit event
│   │   └── ratelimit_test.go
│   ├── ingest/
│   │   ├── event.go                    # RawEvent + EnrichedEvent (34 fields incl. site_id)             [shipped]
│   │   ├── fastreject.go               # POST-only + prefetch/UA-shape gate as chi middleware           [shipped]
│   │   ├── handler.go                  # POST /api/event; ClientIP exported; Audit nil-safe field       [shipped]
│   │   ├── handler_test.go             # 10-case fast-reject table                                       [shipped]
│   │   ├── wal.go                      # tidwall/wal, 100ms fsync, 10GB cap                              [shipped]
│   │   └── consumer.go                 # Dual-trigger batch writer (1000 rows / 500ms / 10MB)            [shipped]
│   ├── enrich/
│   │   ├── pipeline.go                 # 6-worker pipeline (identity→bloom→geo→ua→bot→channel)          [shipped]
│   │   ├── channel.go                  # 17-step decision tree; reload via main.go's runSIGHUP          [shipped]
│   │   ├── geoip.go                    # IP2Location wrapper (no-op fallback when no BIN configured)    [shipped]
│   │   ├── ua.go                       # medama-io/go-useragent singleton                                [shipped]
│   │   ├── bot.go                      # Cheap-first matcher + embedded crawler-user-agents.json        [shipped]
│   │   ├── newvisitor.go               # Bloom filter (18MB / 10M / 0.1% FPR) + cross-day grace         [shipped]
│   │   ├── crawler-user-agents.json    # Embedded bot patterns (refresh via make refresh-bot-patterns)  [shipped]
│   │   └── *_test.go                   # Per-component unit tests                                        [shipped]
│   ├── identity/
│   │   ├── hash.go                     # BLAKE3-128 visitor hash + SHA-256 user_id hash                 [shipped]
│   │   ├── salt.go                     # IRST daily salt (HMAC-SHA256), 5-min overlap, in-mem cache    [shipped]
│   │   └── identity_test.go                                                                              [shipped]
│   ├── storage/
│   │   ├── clickhouse.go               # 34-col batch insert (incl. site_id) + 1 retry                  [shipped]
│   │   ├── migrate.go                  # Templated migrations, schema_migrations bookkeeping            [shipped]
│   │   ├── migrations/                 # SQL embedded via go:embed (lives here, not under clickhouse/)
│   │   │   ├── 001_initial.sql         # events_raw + sites + schema_migrations                          [shipped]
│   │   │   └── 002_rollups.sql         # hourly_visitors + daily_pages + daily_sources + MVs            [shipped]
│   │   ├── store.go                    # Typed Store interface + ErrNotImplemented                     [shipped]
│   │   ├── queries.go                  # whereTimeAndTenant + 6 v1 query implementations               [shipped]
│   │   ├── filter.go                   # Filter struct + Validate + BLAKE3 Hash                         [shipped]
│   │   ├── result.go                   # Typed per-endpoint result structs                              [shipped]
│   │   ├── cached_store.go             # LRU decorator with per-endpoint TTL                            [shipped]
│   │   └── storagetest/                # SeedEvents + CleanSiteEvents helpers (test-only)               [shipped]
│   ├── sites/
│   │   └── sites.go                    # Hostname → site_id lookup; slug/create/disable in Phase 11    [shipped]
│   ├── health/
│   │   └── check.go                    # /healthz (CH ping + WAL fill + uptime)                         [shipped]
│   ├── cache/                          # LRU (realtime=10s / today=60s / historical=∞) + ResolveTTL    [shipped]
│   │   ├── lru.go                      # Thread-safe cache with per-entry expiresAt TTL
│   │   └── policy.go                   # TTL tier constants + ResolveTTL pure function
│   ├── dashboard/                      # 8 GET /api/stats/* + /api/realtime/visitors + bearer-token mw [shipped]
│   │   ├── filter.go                   # ?site / ?from / ?to (IRST→UTC) → storage.Filter
│   │   ├── stats.go                    # 8 handlers (3 return 501 until v1.1/v2)
│   │   ├── realtime.go                 # GET /api/realtime/visitors (10s cache via CachedStore)
│   │   ├── errors.go                   # writeError + classifyError (400 / 501 / 500 + audit)
│   │   ├── auth.go                     # BearerTokenMiddleware (stub — Phase 2b replaces wholesale)
│   │   └── router.go                   # Mount(chi.Router, Deps) — caller decides middleware stack
│   │   # admin/* + signup/* + billing/* routes wait on                                   [planned: Phase 3c + 11]
│   └── auth/                           # bcrypt sessions + RBAC (admin / viewer / api)                 [planned: Phase 2b]
├── web/                                # Preact SPA (Vite + TypeScript + @preact/signals)              [planned: Phase 5]
├── tracker/                            # <2KB IIFE tracker (sendBeacon + history API)                  [planned: Phase 4]
├── clickhouse/
│   └── schema.sql                      # Reference DDL pointer to internal/storage/migrations/         [shipped]
├── config/
│   ├── statnive-live.yaml              # Defaults: server, clickhouse, ingest, enrich, tls, audit,     [shipped]
│   │                                   # ratelimit, license. master.key path.
│   └── sources.yaml                    # 60+ Iranian + AI referrer entries                              [shipped]
├── deploy/
│   ├── docker-compose.dev.yml          # Local dev ClickHouse (named volumes, 127.0.0.1 only)          [shipped]
│   ├── statnive-live.service           # systemd unit (NoNewPrivileges, ProtectSystem)                 [planned: Phase 2c]
│   ├── iptables.sh                     # Firewall rules (80/443/22; CH never exposed)                  [planned: Phase 2c]
│   ├── backup.sh                       # clickhouse-backup + age + zstd                                 [planned: Phase 2c]
│   ├── airgap-install.sh               # One-shot offline installer                                    [planned: Phase 8]
│   └── airgap-update-geoip.sh          # Offline GeoIP DB rotation                                     [planned: Phase 8]
├── vendor/                             # Vendored Go deps — checked in for offline builds              [shipped]
├── offline-bundle/                     # Release artifact (binary + DB23 + SHA256SUMS + signature)     [planned: Phase 8]
├── docs/
│   ├── tech-docs/                      # Context7-cached library refs (16 libs)                         [shipped]
│   ├── rules/                          # Extended rationale for CLAUDE.md rules                         [shipped]
│   ├── history/                        # Doc-25/27/28 skill-roster accretion narratives                 [shipped]
│   ├── tooling.md                      # Claude Code skills + MCP setup                                 [shipped]
│   ├── brand.md                        # statnive.live visual identity reference                        [shipped]
│   ├── cli-operator-surface.md         # CLI + MCP v1.1/v2 surfaces                                     [shipped]
│   ├── deployment.md                   # SaaS + air-gap + server costs                                  [shipped]
│   ├── tech-docs-index.md              # Context7 cache index                                           [shipped]
│   └── repo-structure.md               # This file                                                      [shipped]
├── test/
│   ├── integration_test.go             # 100-event smoke (handler → WAL → CH)                          [shipped]
│   ├── enrichment_e2e_test.go          # All 6 stages produce expected events_raw columns              [shipped]
│   ├── multitenant_isolation_test.go   # Privacy Rule 2: per-tenant visitor_hash separation            [shipped]
│   ├── security_test.go                # Rate limit short-circuits before events reach ClickHouse      [shipped]
│   ├── dashboard_isolation_test.go     # Architecture Rule 8: every Store query scoped by site_id     [shipped]
│   ├── tls_keys/                       # Self-signed cert+key (make tls-test-keys)                     [shipped]
│   └── k6/load-test.js                 # 7K EPS smoke                                                  [planned: Phase 7]
├── Makefile                            # build, test, test-integration, lint, fmt, vendor-check,       [shipped]
│                                       # licenses, tenancy-grep, dev-secret, tls-test-keys,
│                                       # refresh-bot-patterns. airgap-bundle / release stubs.
├── go.mod
├── go.sum
└── README.md                           # Operator quick-start                                          [planned]
```
