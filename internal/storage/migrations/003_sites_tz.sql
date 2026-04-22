-- 003_sites_tz.sql — per-site timezone column for IRST/UTC date boundary.
-- Iranian deployments default to Asia/Tehran (doc 24 § Sec 2 IRST contract);
-- SaaS tenants outside Iran override to their operator-local zone so the
-- dashboard's date-picker midnight boundaries match what site owners expect.
-- Templated for single-node (current) and Distributed (v1.1+) using the
-- same Go text/template convention as 001_initial.sql and 002_rollups.sql.

ALTER TABLE statnive.sites{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
    ADD COLUMN IF NOT EXISTS tz LowCardinality(String) DEFAULT 'Asia/Tehran';
