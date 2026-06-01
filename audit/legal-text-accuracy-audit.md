# Legal Text Accuracy + Cross-Template Consistency Audit — Stream B aggregator

> **Generated:** 2026-06-01 · **Aggregator:** 3 Explore reviewers (B1–B3) ran in parallel · **Inputs:** the 6 legal templates in `internal/legal/templates/` + `docs/dpa-draft.md` + my earlier televika playbook drafts (Brussels Court, EDPB-EDPS Opinion 2/2026 citations).
>
> **Scope:** Re-verify every legal citation against the actual ruling text; cross-check retention numbers, sub-processor lists, legal bases, endpoint shapes, cookie names across all templates; verify every technical claim against the code.

---

## Summary

**17 discrepancies catalogued · 5 HIGH severity · 8 MEDIUM · 4 LOW.** All HIGH items must be fixed before televika's Anwalt signs off. PR plan queued at `~/.claude/plans/template-fixes-from-audit.md`.

---

## HIGH severity (5) — must fix before televika ships Path A

### HIGH-1 — Raw event retention: DPA "30 days" vs Notice/LIA "180 days"

**Templates affected:** `dpa.md` § 3 (says 30) vs `privacy_policy_{de,en}.md` (says 180) vs `lia_{de,en}.md` (says 180).

**Code ground truth:** 180 days. `internal/storage/migrations/001_initial.sql:86` sets `TTL time + INTERVAL 180 DAY DELETE`. The DPA is wrong.

**Patch hunk (statnive-live):**
```diff
--- a/internal/legal/templates/dpa.md
+++ b/internal/legal/templates/dpa.md
@@ -45,3 +45,3 @@
-| Visitor identifier (BLAKE3-128 hash of `master_secret \|\| site_id \|\| user_id`) | `FixedString(16)` | Raw event 30 days; rollups indefinite (HLL state, anonymous per Recital 26 + CJEU C-413/23) |
+| Visitor identifier (BLAKE3-128 hash of IP \|\| UA keyed by daily salt) | `FixedString(16)` | Raw event 180 days; rollups 750 days (CNIL Sheet 16 audience-measurement ceiling) |
```

### HIGH-2 — Endpoint shape mismatch

**Templates affected:** `dpa.md` § 5.5 says `GET /api/privacy/export?user_id=…` + `DELETE /api/privacy/erase?user_id=…`. Notices + LIAs say `GET /api/privacy/access` + `POST /api/privacy/erase` + `POST /api/privacy/opt-out` + `POST /api/privacy/consent`.

**Code ground truth:** notices are correct. DPA endpoints don't exist.

**Patch hunk:**
```diff
- `GET /api/privacy/export?user_id=…` — visitor-scoped data export (CSV / JSON).
- `DELETE /api/privacy/erase?user_id=…` — visitor-scoped erasure across raw + rollup tables (CASCADE), with `system.tables` enumerated dynamically so a forgotten table fails the integration test by construction (per `PLAN.md:585` DSAR completeness gate).
+ `GET /api/privacy/access` — visitor data envelope (requires `_statnive` cookie auth).
+ `POST /api/privacy/erase` — async 202; ALTER TABLE … DELETE across all MergeTree tables carrying `cookie_id` via `system.tables` enumeration; per-site scoped (WHERE `cookie_id = ? AND site_id = ?`).
+ `POST /api/privacy/opt-out` — sets `_statnive_optout_<site_id>=v1`; works without prior `_statnive` cookie (post-v0.0.35).
+ `POST /api/privacy/consent {"action":"give"\|"withdraw"}` — give mints `_statnive` UUID if absent; withdraw clears cookies + adds hash to suppression list.
```

### HIGH-3 — Hash algorithm confusion (BLAKE3 vs SHA-256 attribution)

**Templates affected:** DPA + LIA-DE say "BLAKE3-128"; privacy_policy_{de,en} say "SHA-256 hash" of the `_statnive` cookie. Both are true but for DIFFERENT identifiers — templates conflate them.

**Code ground truth:** 3 separate identifiers, 2 algorithms:
- `visitor_hash` = `BLAKE3-128(IP || "|" || UA)` keyed by daily HMAC salt → `FixedString(16)`
- `user_id_hash` = `SHA-256(master_secret || site_id || user_id)` → `String`
- `cookie_id` = `"h:" + hex(SHA-256(master_secret || site_id || _statnive UUID))` → `String`

**Patch hunk:** Add a "Three identifiers" table to privacy_policy_{de,en}.md + DPA § 3:
```diff
+ | Identifier | Algorithm | Input | Lifetime | At-rest column |
+ |---|---|---|---|---|
+ | visitor_hash | BLAKE3-128, daily-salted (HMAC-SHA256 derived) | IP \|\| "\|" \|\| UA | 24h (cross-day unlinkable) | `events_raw.visitor_hash` FixedString(16) |
+ | user_id_hash | SHA-256, tenant-scoped | master_secret \|\| site_id \|\| user_id | Persistent | `events_raw.user_id_hash` String |
+ | cookie_id | SHA-256, tenant-scoped, "h:" prefix | master_secret \|\| site_id \|\| `_statnive` UUID | Bound to cookie lifetime (1 year) | `events_raw.cookie_id` String |
```

### HIGH-4 — Sub-processor names missing from privacy notices

**Templates affected:** `privacy_policy_{de,en}.md` + `lia_{de,en}.md` — none of the 4 customer-facing templates name Netcup, Cloudflare, ISRG, MailerLite by name. Only `dpa.md` Schedule A links to the register.

**Art. 13(1)(e) GDPR requires controllers to disclose "recipients or categories of recipients" of personal data.** Categories may be acceptable if specifics are impractical, but a single-entity sub-processor (Netcup) should be named.

**Patch hunk:** add to `privacy_policy_{de,en}.md` after the "Wir nutzen `statnive.live` … als Auftragsverarbeiter" line:
```markdown
**Sub-Auftragsverarbeiter:** statnive.live wird auf der Infrastruktur der Netcup GmbH
(Nürnberg, Deutschland) betrieben. Die vollständige Liste der Sub-Auftragsverarbeiter
ist in Schedule A der AVV unter `/legal/dpa` einsehbar.
```

### HIGH-5 — 4 of 11 Art. 13 rights missing from privacy notices

**Templates affected:** `privacy_policy_{de,en}.md` enumerate only Art. 15 (access), 17 (erasure), 21 (objection). **Missing:** Art. 16 (rectification), 18 (restriction), 20 (portability), 22 (automated decision-making).

**EDPB March 2026 CEF Action** specifically targets incomplete Art. 13 notices across all 25 EU DPAs. This is enforcement-priority.

**Patch hunk:** add the 4 missing right paragraphs (text drafted in `televika-PATH-A-EVIDENCE.md` § C.3 for paste into the canonical template).

---

## MEDIUM severity (8) — fix before next quarter

### MED-1 — "Rotating salt with previous-file deletion" wording is misleading

**Templates:** `privacy_policy_{de,en}.md`, `lia_{de,en}.md` — imply file-based salt rotation with deletion. **Code:** salt is derived on-the-fly via HMAC, no files exist (privacy posture is STRONGER than stated).

**Patch:** Replace "rotierend" / "rotating" with "abgeleitet via HMAC-SHA256" / "derived via HMAC-SHA256, never persisted to disk".

### MED-2 — "Vor Ihrer Einwilligung" language assumes a banner

**Templates:** `privacy_policy_{de,en}.md` § Hybrid-Einwilligung. Implies operator shows a consent banner — misleading for no-banner Path A deployments.

**Patch:** branch the text into 2 paragraphs: (a) "Bei Operatoren, die einen Banner einsetzen…" + (b) "Bei Operatoren, die keinen Banner einsetzen (z.B. Path A LI-basiert)…".

### MED-3 — GPC/DNT disclosure missing from templates

**Templates:** None of the 6 templates explicitly say "Sec-GPC + DNT werden serverseitig respektiert wenn vom Operator aktiviert." Code respects them when `respect_gpc/dnt=1` per `internal/ingest/handler.go:269-274`.

**Patch:** add paragraph to privacy notices.

### MED-4 — DPA § 3 claims `daily_geo`/`daily_devices` rollups (not shipped in v1)

These rollups are CLAUDE.md "v1.1" deferred. DPA lists them as if shipped. Either remove or note "v1.1 planned".

### MED-5 — DPA § 5.3 says "BLAKE3-128 hashing" without distinguishing the three identifier classes

Same problem as HIGH-3 but in § 5.3 context. Patch in the same revision.

### MED-6 — Brussels Court 14.05.2025 cited as authority for no-banner posture (in my earlier playbook drafts, not in canonical templates)

**Source:** my earlier conversational drafts. **W2D verified:** the ruling is TCF-scope only. **Patch:** when citing, frame as "Brussels Court of Appeal 14.05.2025 confirmed TC Strings + IP = personal data under CJEU C-604/22; this confirms statnive treats hashed visitor signatures as personal data, not as anonymous". Do NOT cite as authority for the no-banner posture.

### MED-7 — EDPB Guidelines 01/2025 cited as binding

**Templates:** `dpa.md` § 3 footnote. **Status:** DRAFT, public consultation through 28 Feb 2025; final version pending.

**Patch:** add "(draft, January 2025; final pending)" qualifier.

### MED-8 — DPA + RoPA accountability — Art. 30 RoPA not addressed in customer-facing texts

Controllers (operators) MUST maintain Art. 30 RoPA. None of statnive's templates remind operators of this obligation. Add a footnote / link to the canonical CNIL/BfDI RoPA template.

---

## LOW severity (4) — cosmetic / wording polish

### LOW-1 — BGH I ZR 7/16 *Planet49* date conflation possible

CJEU C-673/17 was 1 Oct 2019; BGH follow-up 28 May 2020. If any template / playbook cites "Planet49 (28 May 2020)" as the CJEU ruling, separate the two.

### LOW-2 — DPA controller-identity hardcoded

`dpa.md:18` hardcodes "operated by Parhum Khoshbakht, customer 365334 of Netcup GmbH". This is correct for statnive but means the DPA isn't a fully-generic template. Operator gets a pre-filled processor block.

### LOW-3 — "Datenschutz-Einstellungen" link wording

My earlier playbook recommended "Datenschutz-Einstellungen". noyb scanner is BANNER-focused so this label isn't a complaint trigger, but "Widerspruch (Tracking-Opt-Out)" is more explicit. (Operator-side choice, not a template change.)

### LOW-4 — privacy_page.html operator-facing only

The hosted consent UI at `/legal/privacy` is operator-facing UX. Acceptable for v1; could grow operator-side controls in v1.1.

---

## Cross-template consistency matrix (abbreviated B1 output)

Full 14-row consistency table is in the B1 reviewer's transcript. Critical contradictions consolidated above (HIGH 1–5).

---

## Path A operator implications

For televika.com specifically:

1. **HIGH-1 to HIGH-5** are all in the canonical statnive templates. The operator copies/cites those templates — so the operator inherits the inaccuracies. Either wait for the PR to land before signing the DPA, or sign-with-side-letter.
2. **MED-2 (banner language)** is the biggest live UX issue: privacy_policy_de.md "Vor Ihrer Einwilligung" implies a banner exists. For televika's no-banner Path A, this language must be customised on the operator's site (templates have it wrong, but operator can patch in their own copy).
3. **HIGH-4 (sub-processor names)** + **HIGH-5 (4 missing rights)** are paste-text fixes the operator can do TODAY without waiting for statnive-live PR.
4. Citations: don't quote Brussels Court 14.05.2025 as authority for no-banner posture. Brussels Court ruling is real and supports pseudonymisation framing only.

---

## Methodology

| Reviewer | Scope |
|---|---|
| B1 | Cross-template consistency: 14-row claim table × DPA · LIA-DE · LIA-EN · Notice-DE · Notice-EN · privacy_page.html |
| B2 | Legal citation verification: 16 citations traced to source rulings/guidance. ✅ ACCURATE / ⚠️ OVER-REACH / ❌ NONEXISTENT / ⚠️ PROPOSED-NOT-LAW |
| B3 | Technical claim ↔ code accuracy: 17 claims, 16 ACCURATE, 1 INACCURATE (rotating-salt wording) |

W4 verification cross-checks: Stream C W2B verified EDPB Guidelines 01/2025 status; Stream C W2D verified EDPB-EDPS Joint Opinion 2/2026 EXISTS (B2 was wrong on that one) and Brussels Court 14.05.2025 scope.
