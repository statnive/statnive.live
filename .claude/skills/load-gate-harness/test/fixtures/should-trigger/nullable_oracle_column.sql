-- Should trigger no-nullable-on-oracle-columns — Nullable(UUID) is banned.
ALTER TABLE statnive.events_raw
    ADD COLUMN test_run_id Nullable(UUID) CODEC(ZSTD(1)),
    ADD COLUMN generator_node_id Nullable(UInt16) CODEC(ZSTD(1));