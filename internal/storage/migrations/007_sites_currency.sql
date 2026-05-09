-- 007_sites_currency.sql — per-site currency for revenue/RPV reporting.
-- Stored as ISO 4217 alpha-3 (USD, EUR, IRR, ...). LowCardinality keeps
-- it cheap on the sites read path (every dashboard query touches sites
-- once via Lookup). Default 'EUR' for new and existing sites — operators
-- post-deploy PATCH per-site currency to its real value via
-- /api/admin/sites/{id} (currency is a display-only label; switching
-- relabels existing integers without transforming stored values).
--
-- Lossless: existing rows pick up the default at read time; no UPDATE,
-- no row rewrites. Templated for single-node (current) and Distributed
-- (v1.1+) using the same Go text/template convention as 001/002/003/006.

ALTER TABLE statnive.sites{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
    ADD COLUMN IF NOT EXISTS currency LowCardinality(String) DEFAULT 'EUR';
