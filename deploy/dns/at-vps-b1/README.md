# NSD authoritative DNS for `statnive.ir` (AT-VPS-B1)

Production-readiness work-package **A6** from
[`~/.claude/plans/deep-review-for-ready-declarative-giraffe.md`](../../../../../.claude/plans/deep-review-for-ready-declarative-giraffe.md).

This directory provisions a self-hosted NSD authoritative primary
serving `<customer>.statnive.ir` from inside Iran (the AT-VPS-B1
bastion at Asiatech). Architecture C, single-primary, no Cloudflare,
no AXFR-out — per the `iranian-dc-deploy` skill items 1 + 13 +
[`CLAUDE.md` § Architecture / DNS](../../../../CLAUDE.md).

## Why not Cloudflare

OFAC `31 CFR 560.540(b)(3)` + no Iranian POP. `.ir` zones MUST stay
inside Iran or on a non-sanctioned outside-Iran name server that
accepts Iranian customer data. Cloudflare doesn't. So we run our own
NSD primary on AT-VPS-B1.

## Why single-primary, no AXFR-out

PLAN.md Architecture C carve-out for the disjoint-customer-set case.
A secondary inside Iran is a v1.1 item — the resilience win against
AT-VPS-B1 dying is high but the operational complexity (key rotation,
NOTIFY flow, AXFR/IXFR perms) doesn't fit the dry-run window.

## Files

| File | Purpose |
|---|---|
| `nsd.conf.template` | NSD4 server config — single-primary, no remote-control, no AXFR-out. Drops into `/etc/nsd/nsd.conf` on the box. |
| `statnive.ir.zone.template` | Zone file with `{{PLACEHOLDERS}}` for the operator's NS IPv4/v6 + each customer's slug + VPS IPs. Drops into `/etc/nsd/zones/statnive.ir.zone`. |
| `install.sh` | Idempotent provisioner — substitutes placeholders, validates with `nsd-checkconf` + `nsd-checkzone`, enables + reloads. |
| `README.md` | This file. |

## First-install procedure

On a fresh Asiatech G2 VPS (the "AT-VPS-B1" bastion):

1. SSH in as root.
2. Copy this directory onto the box (via `scp -C -r` from your laptop,
   or via the `airgap-bundle` if you choose to include `deploy/dns/`
   in the bundle — currently NOT included since this is operator-side
   infrastructure, not customer-binary).
3. Run the installer:

   ```bash
   sudo PRIMARY_NS_IPV4=<at-vps-b1-public-ipv4> \
        PRIMARY_NS_IPV6=<at-vps-b1-public-ipv6> \
        ADMIN_EMAIL=ops@statnive.ir \
        CUSTOMER_SLUG=sampleplatform \
        CUSTOMER_VPS_IPV4=<sampleplatform-asiatech-ipv4> \
        CUSTOMER_VPS_IPV6=<sampleplatform-asiatech-ipv6> \
        ZONE_SERIAL=$(date -u +%Y%m%d)01 \
        ./install.sh
   ```

4. The installer ends by printing the IRNIC glue records to register
   manually at https://www.nic.ir/. Bring the `ns1.statnive.ir` glue
   + DS records (DNSSEC is v1.1) into IRNIC; expect 24h propagation.

## Add a second customer

Same procedure — re-run `install.sh` with the new `CUSTOMER_SLUG=` +
`CUSTOMER_VPS_IPV4=` + **bumped `ZONE_SERIAL=`** (critical — without
the bump, v1.1 secondaries will silently drop the change). Example:

```bash
sudo PRIMARY_NS_IPV4=<at-vps-b1-public-ipv4> \
     PRIMARY_NS_IPV6=<at-vps-b1-public-ipv6> \
     ADMIN_EMAIL=ops@statnive.ir \
     CUSTOMER_SLUG=secondcustomer \
     CUSTOMER_VPS_IPV4=<secondcustomer-asiatech-ipv4> \
     CUSTOMER_VPS_IPV6=<secondcustomer-asiatech-ipv6> \
     ZONE_SERIAL=$(date -u +%Y%m%d)02 \
     ./install.sh
```

The current template hard-codes ONE customer block; for >1 customer
either:

- Hand-edit the substituted `/etc/nsd/zones/statnive.ir.zone` to add
  another A/AAAA pair, bump SOA serial, `systemctl reload nsd`.
- Or refactor the template + installer to accept a customer list (v1.1).

## Verify reachability from outside Iran

```bash
# From your operator laptop:
dig @<at-vps-b1-public-ipv4> SOA statnive.ir
dig @<at-vps-b1-public-ipv4> A sampleplatform.statnive.ir

# After IRNIC propagates (~24h):
dig SOA statnive.ir   # any resolver
dig A sampleplatform.statnive.ir
```

## Verify reachability from inside Iran during a BGP cut

The whole point of running NSD inside Iran. From an Iranian resolver
(Asiatech's, or any `1.x.x.x` ISP DNS), during a normal connectivity
state:

```bash
dig @<asiatech-resolver-ip> SOA statnive.ir
```

During a simulated BGP cut (test pre-cutover, NOT in production), the
zone should keep answering Iranian resolvers via NIN. Phase 7e chaos
scenario #1 covers this; document the result in the cutover evidence
pack.

## Operational notes

- **Zone serial**: bump `ZONE_SERIAL` on every change (RFC 1912). The
  installer defaults to `$(date -u +%Y%m%d)01` if unset; bump the
  `01` suffix on multiple same-day changes.
- **CAA records**: lock cert issuance to `letsencrypt.org` +
  `sectigo.com`. Cert-forge (outside Iran) issues against these
  CAs; the lock prevents a rogue CA from issuing for `statnive.ir`.
- **MX**: explicitly omitted — `support@statnive.com` is the
  canonical mailbox (CLAUDE.md statnive-fa / statnive-ar invariants).
  Do NOT add MX without first updating that invariant.
- **DNSSEC**: not enabled in v1. Phase 10 polish — pre-sign offline
  with `ldns-signzone`, ship the signed zone via the same installer.
- **IPv6 is optional**: leave `PRIMARY_NS_IPV6=` and/or
  `CUSTOMER_VPS_IPV6=` unset on a v4-only Asiatech allocation; the
  installer drops the matching AAAA records from the rendered zone
  rather than producing malformed RDATA.
- **Backup**: zone file + nsd.conf are the entire state (~5 KB).
  `tar /etc/nsd /var/lib/nsd` daily is sufficient — zone changes
  on customer onboarding land at unpredictable cadence and "weekly"
  may miss a 6-day-old onboarding. v1.1 deliverable: ship
  `deploy/dns/at-vps-b1/backup-nsd.cron` alongside the A8
  ClickHouse-backup track. Restore is `tar -x` + `systemctl
  restart nsd`.
- **Secondary NS** (v1.1): add a second VPS inside Iran (ParsPack or
  Shatel), enable AXFR-out to its IP only, register NS at IRNIC.

## Related CLAUDE.md rules

- **Never ArvanCloud** — sanctioned, 2022 breach. AT-VPS-B1 is on
  Asiatech AS43754; the secondary in v1.1 should be ParsPack or
  Shatel, not ArvanCloud.
- **No Cloudflare in IR-resident code paths** — `.ir` zone never sees
  Cloudflare.
- **`iran-no-letsencrypt-in-binary`** — Let's Encrypt is in the CAA
  allow-list, but ACME runs OUTSIDE Iran (cert-forge bastion, A5);
  the binary itself never dials ACME.
