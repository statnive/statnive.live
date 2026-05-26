# Deployment postures

`statnive-live` runs the **same binary** in three deployment topologies. The posture is an informational label (env var `STATNIVE_POSTURE`, config key `posture`) that the binary announces at boot and surfaces in `/healthz` + audit — every behavioral knob is independently configured per CLAUDE.md Architecture Rule 5. The label exists so "wrong posture / wrong knobs" drift is visible at a glance.

## The three canonical postures

| Posture | Operator | Tenancy | License | Outbound | NTP | TLS source |
| --- | --- | --- | --- | --- | --- | --- |
| `saas` | statnive.live (us) | multi-tenant | none | unrestricted | distro default | manual rotation or ACME via reverse proxy |
| `outside-iran` | customer (their VPS) | single-tenant | required | unrestricted | distro default | manual rotation or ACME |
| `inside-iran` | customer (Asiatech) | single-tenant | required | `iptables -P OUTPUT DROP` + allowlist | `chrony.conf.asiatech` (Iranian sources) | rsync from outside-Iran cert-forge bastion |

Empty posture (`""`) is the legacy/dev fallback — it loads with defaults, logs `"deployment posture unset"`, and never gates anything. Anything other than the three canonical values or empty is a boot-blocking error.

## When to pick which

- **SaaS**: we host on Netcup / Hetzner / etc. Multi-tenant across customer `site_id`s. GDPR applies because EU visitors hit our boxes — `consent.required=true`, customer DPA, sub-processor register at `docs/compliance/subprocessor-register.md`.
- **Outside-Iran**: customer hosts on their own VPS (Hetzner / AWS / bare metal). Single-customer per binary, license JWT required. ACME / Let's Encrypt allowed; standard outbound. `consent.required` defaults true; flip false if the customer has no EU visitors.
- **Inside-Iran**: customer hosts on Asiatech inside Iran. Air-gap-capable. License JWT required. `consent.required=false` (Iran has no GDPR — Privacy Rule 5 carve-out). Outbound DROP via `--apply-iptables`. NTP via `chrony.conf.asiatech`. TLS via outside-Iran `cert-forge` rsync (no ACME-from-Iran per CLAUDE.md anti-patterns).

## The "same binary" invariant

The Go binary does **NOT** branch on the posture string at runtime. There are no build tags, no posture-conditional code paths, no `if posture == "inside-iran"` blocks. Every behavioral difference between the three rows above is driven by independently-configured knobs:

- `license.file` set → license verify enabled; empty → no verify (both code paths compiled in)
- `consent.required` flips GDPR-on / GDPR-off
- `outbound.allowlist` populated → opt-in outbound features available; empty → air-gap default
- `chrony.conf` on the host → NTP behavior (operator-installed, not in binary)
- iptables rules → outbound DROP (operator-installed, not in binary)

This means an operator can mismatch posture and knobs — declare `inside-iran` but leave `consent.required=true`, for example. The posture announce in `/healthz` makes that drift visible to monitoring, and integration tests (`test/posture_drift_test.go` planned) gate against the most common mistakes.

## Posture vs the codebase

| Layer | What it does with posture |
| --- | --- |
| `cmd/statnive-live/main.go` | Validates posture is in allowlist; logs the announce at boot; otherwise ignores |
| `config/statnive-live.yaml.example` | Documents the three canonical postures with their knob recipes |
| `deploy/airgap-install.sh --posture=X` | Sets `STATNIVE_POSTURE=X` in the systemd drop-in; `inside-iran` implies `--ntp-profile=asiatech` + `--apply-iptables` |
| `deploy/courier.sh POSTURE=X` | Conditional license install (skip for `saas`); passes `--posture=X` to airgap-install |
| `Makefile release-customer` | Operator-facing wrapper; takes `POSTURE=` |
| `.github/workflows/posture-matrix.yml` | 3-job CI matrix gates every PR per posture |
| `.github/workflows/release.yml` | Renders per-posture install commands into the release notes |

## How to add a fourth posture (don't)

This is intentionally hard. Adding `outside-iran-saas` or `inside-iran-dr` is a contract change that ripples through six layers (the table above). Before adding one:

1. Confirm it can't be expressed as a knob combination on an existing posture.
2. Update `cmd/statnive-live/main.go:validPostures` AND the example config comment block AND the airgap-install allowlist AND the courier allowlist AND the workflow matrix list AND the release-notes template.
3. Add a row to the table at the top of this file with knob defaults.
4. Add a customer-onboarding entry to `docs/customer-onboarding.md`.

If you find yourself adding a posture every few releases, the abstraction is wrong — collapse them or generalize to free-form tags.

## See also

- `docs/runbook.md § Phase 10` — inside-iran cutover SOP
- `docs/runbook.md § Phase 10b` — outside-iran cutover SOP
- `docs/customer-onboarding.md` — operator-facing customer onboarding flow
- `docs/rules/privacy-detail.md § Rule 5` — Iran no-GDPR carve-out
- `docs/rules/netcup-vps-gdpr.md` — SaaS ops contract
- CLAUDE.md § "Privacy Rules" — both postures' GDPR posture in one place
