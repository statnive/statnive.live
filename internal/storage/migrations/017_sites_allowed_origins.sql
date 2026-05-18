-- 017_sites_allowed_origins.sql — per-site CORS allowlist for cross-origin
-- SaaS consent flows.
--
-- Stage 4 cross-origin support. The Stage-3 hybrid-consent flow assumes
-- same-origin (the operator self-hosts statnive-live on track.<their
-- domain>). For SaaS where the operator's site (e.g. televika.com)
-- loads the tracker from a different origin (app.statnive.live),
-- /api/privacy/{consent,opt-out,erase,access} need CORS to land in
-- the browser. This column holds the per-site origin allowlist that
-- internal/middleware/cors.go reads through internal/sites/origin_index.go.
--
-- Value shape. JSON-encoded []string (mirrors event_allowlist from 013).
-- Each entry is an RFC 6454 origin: scheme + host + optional port,
-- no path / query / fragment. Validation lives in Go
-- (sites.validateAllowedOrigins) via url.Parse + idna.Lookup.ToASCII
-- so IDN edge cases and IPv6 literals get the same treatment they
-- would in a browser. Empty list = same-origin only (current behaviour).
--
-- Existing-site preservation. Migration backfills empty list for every
-- existing row — the 3 live operators (statnive.com, statnive.de,
-- fr.statnive.com) keep working with the same-origin flow until they
-- consciously add origins via the admin PATCH. No HTTP behaviour change
-- ships with PR 4-A alone; PR 4-B activates CORS using this column.
--
-- Forward-only (project convention — no paired down migration). To
-- roll this back manually after a Stage-4 revert, an operator runs:
--
--   ALTER TABLE statnive.sites DROP COLUMN IF EXISTS allowed_origins;
--
-- against the live ClickHouse instance. The Go code in
-- internal/sites/sites.go's SELECTs must already be reverted before the
-- DROP, otherwise LookupSitePolicy fails on every event.

ALTER TABLE statnive.sites{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
    ADD COLUMN IF NOT EXISTS allowed_origins String DEFAULT '[]';

ALTER TABLE statnive.sites{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
    UPDATE allowed_origins = '[]'
    WHERE allowed_origins = '';
