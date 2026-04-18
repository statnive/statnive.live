---
title: spf13/viper Documentation
library_id: /spf13/viper
type: context7-reference
created: 2026-04-17
updated: 2026-04-17
context7_mode: code
topic: yaml-hot-reload
tags: [context7, viper, config, yaml, hot-reload, fsnotify]
source: Context7 MCP
cache_ttl: 7 days
---

# spf13/viper — YAML config + hot reload (confirmed MIT-licensed replacement for koanf)

## Hot-reload pattern for statnive-live goals/funnels

```go
import (
    "github.com/fsnotify/fsnotify"
    "github.com/spf13/viper"
)

v := viper.New()
v.SetConfigFile("./config/statnive-live.yaml")
v.SetConfigType("yaml")

if err := v.ReadInConfig(); err != nil {
    log.Fatalf("config read: %v", err)
}

v.OnConfigChange(func(e fsnotify.Event) {
    log.Printf("config changed: %s", e.Name)
    if err := v.ReadInConfig(); err != nil {
        log.Printf("config re-read failed: %v", err)
        return
    }
    // Rebuild goals/funnels from new config — NO restart needed
    reloadGoals(v.Get("goals"))
    reloadFunnels(v.Get("funnels"))
})

v.WatchConfig()
```

## CRITICAL: `AddConfigPath` must come BEFORE `WatchConfig()`

```go
// All config paths must be defined prior to calling WatchConfig()
v.AddConfigPath("/etc/statnive-live")
v.AddConfigPath("./config")
v.WatchConfig()
```

## SIGHUP alternative (PLAN.md's stated mechanism)

PLAN.md mentions "Config changes to goals/funnels hot-reload via SIGHUP (no restart)". `viper.WatchConfig()` uses fsnotify (file-system events), not SIGHUP. **Decision:** use fsnotify by default — no manual signal needed. Keep SIGHUP as a forced-reload fallback.

## License confirmed: MIT

Viper is MIT-licensed. Confirmed safe for statnive-live's SaaS-outside-Iran distribution constraint (PLAN.md § License Rules).

## No API deltas vs 2026-04-17 snapshot.
