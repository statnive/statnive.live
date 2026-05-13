-- 014_users_jurisdiction_notice.sql — per-user dismissal flag for the
-- Stage-3 one-time admin notice ("Set your jurisdiction in Site
-- Settings → Compliance to enable consent-free / hybrid mode").
--
-- The 3 live operators land on backfilled OTHER-NON-EU + permissive
-- after migration 013; their tracking behaviour is byte-for-byte
-- identical until they consciously pick a jurisdiction. The notice
-- prompts them once on first dashboard load after Stage 3 ships;
-- dismissing it sets this flag so the prompt never reappears for that
-- user. Default 0 = not yet dismissed, so every existing admin gets
-- the prompt exactly once.
--
-- ReplacingMergeTree(updated_at) on users handles the dismissal write
-- the same way it handles every other user-row mutation: the
-- dismissal endpoint UPSERTs the row with the same user_id + site_id
-- and a fresh updated_at; FINAL reads converge on the most recent
-- state.

ALTER TABLE statnive.users{{if .Cluster}} ON CLUSTER {{.Cluster}}{{end}}
    ADD COLUMN IF NOT EXISTS jurisdiction_notice_dismissed UInt8 DEFAULT 0;
