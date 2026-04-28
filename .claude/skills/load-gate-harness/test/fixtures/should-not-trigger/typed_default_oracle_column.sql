-- Should NOT trigger no-nullable-on-oracle-columns — typed DEFAULT sentinels.
ALTER TABLE statnive.events_raw
    ADD COLUMN test_run_id UUID DEFAULT toUUID('00000000-0000-0000-0000-000000000000') CODEC(ZSTD(1)),
    ADD COLUMN generator_node_id UInt16 DEFAULT 0 CODEC(ZSTD(1));