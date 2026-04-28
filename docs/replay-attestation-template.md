# Replay-attestation template — Phase {N} graduation gate

> One per graduation phase (P1…P5). Signed by the SamplePlatform analytics owner before the export leaves their S3 / object-store bucket. Without this attestation the export does NOT enter statnive staging.

Spec: [research doc 29 §9 open question 4](../../jaan-to/docs/research/29-Production-load-simulation-gate-statnive-live-asiatech-tehran.md). The replay path complements synth (`test/perf/generator --mode=synth`) — synth provides curve-formula stress; replay provides production-shape realism.

---

## 1 — Phase identification

- **Gate phase:** P{1|2|3|4|5}
- **Date prepared:** YYYY-MM-DD (IRST)
- **Export window:** YYYY-MM-DD HH:MM → YYYY-MM-DD HH:MM (production timezone, conversion to UTC noted)
- **Approximate event count:** {N}
- **Source object-store path:** `s3://...` or equivalent
- **Destination staging path:** `s3://staging-statnive-load-gate/replay/P{N}/...`
- **Chain-of-custody hops:** {list each system the export traverses}

## 2 — Regex-scrub specification

The export was processed through the following transformations BEFORE leaving SamplePlatform's perimeter. Any record matching a "must redact" pattern was replaced by a salted hash; any record matching a "must drop" pattern was elided entirely.

| Pattern | Action | Notes |
|---|---|---|
| Email address (`[a-z0-9._%+-]+@…`) | replace `<email>` | Unconditional drop — never hashed. |
| Iranian national ID (10-digit Sherif) | drop record | Compliance-blocking field. |
| IPv4 / IPv6 (any) | drop record | Privacy Rule 1 — no raw IP enters the test pipeline. |
| `user_id` (raw) | replace with `SHA256(salt || user_id)` | Daily-rotating salt; salt never accompanies the export. |
| Free-text comment fields | replace `<text>` | Volume preserved; content discarded. |
| Tracker payload `event.metadata` | retain as-is | No PII risk in the schema. |

Acknowledged exceptions (if any): {none / list with rationale}

## 3 — Salt rotation per phase

A separate scrubbing salt is provisioned per phase. The salt:

- is generated server-side via `openssl rand -hex 32`
- is held in SamplePlatform's HSM / KMS (NEVER on the export bucket)
- is destroyed automatically `{kill_switch_days}` days after gate sign-off
- is documented in (but not transmitted with) this attestation as `salt_id = {opaque-id}`

## 4 — Auto-delete kill-switch

Bucket lifecycle policy attached to the destination staging path:

```
{paste S3 lifecycle JSON or equivalent — must enforce hard deletion N
days after last access, with no operator-issued retention extension
without a fresh attestation.}
```

Kill-switch verification: `aws s3api get-bucket-lifecycle-configuration --bucket staging-statnive-load-gate` returns the policy above; SamplePlatform analytics owner confirms via signed-out-of-band channel before sign-off.

## 5 — Chain-of-custody log

Every transit hop is recorded here. Hops outside SamplePlatform's perimeter MUST be confirmed by both endpoints before sign-off.

| Step | From | To | Mechanism | Date | Signed off by |
|---|---|---|---|---|---|
| 1 | SamplePlatform raw | SamplePlatform redaction job | internal | | |
| 2 | SamplePlatform redaction | SamplePlatform staging bucket | internal | | |
| 3 | SamplePlatform staging | statnive staging | SCP / aws s3 cp / signed presigned URL | | |
| 4 | statnive staging | replay generator | mount + read-only | | |

## 6 — Phase 7e harness alignment

This template ships with [`test/perf/generator/main.go`](../test/perf/generator/main.go) `--mode=replay`. The generator reads the replay file as NDJSON line-protocol, never logs the body, and pins every emit to the documentation IPv4 range `192.0.2.0/24` so no live IP — even from the redacted export — touches the test target.

## 7 — Sign-off

| Role | Name | Signature | Date |
|---|---|---|---|
| SamplePlatform analytics owner | | | |
| SamplePlatform compliance | | | |
| statnive load-gate operator | | | |

This document is committed to the statnive repo at `releases/load-gate/P{N}/replay-attestation.md` (gitignored — operators copy at sign-off). The repo retains this template only.
