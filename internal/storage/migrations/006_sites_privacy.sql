-- 006_sites_privacy.sql — per-site privacy policy + bot-tracking flags.
--
-- Replaces the global cfg.consent.respect_gpc / cfg.consent.respect_dnt
-- flags (PR #78 flipped both to default false) with per-site columns so
-- multi-tenant operators can serve EU + non-EU customers from the same
-- binary without re-editing config. Adds a track_bots toggle for sites
-- that want to drop bot traffic at the pipeline (default keeps today's
-- behavior — bots are flagged is_bot=1 and written to events_raw).
--
-- Architecture Rule 5 — typed DEFAULT (no Nullable). UInt8 with explicit
-- default matches Architecture Rule 5 carve-out for boolean policy
-- columns. Defaults preserve the post-PR-78 SaaS posture (count every
-- visit, no DNT/GPC suppression) while still allowing operator opt-in
-- per-site for EU compliance.
--
-- Templated for single-node (current) and Distributed (v1.1+) using the
-- same Go text/template convention as 001_initial.sql / 002_rollups.sql /
-- 003_sites_tz.sql.

ALTER TABLE statnive.sites{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
    ADD COLUMN IF NOT EXISTS respect_dnt UInt8 DEFAULT 0,
    ADD COLUMN IF NOT EXISTS respect_gpc UInt8 DEFAULT 0,
    ADD COLUMN IF NOT EXISTS track_bots  UInt8 DEFAULT 1;
