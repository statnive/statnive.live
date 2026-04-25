# DNS zone layout — `statnive.live` + `statnive.ir`

> **⚠️ SUPERSEDED FOR SAMPLEPLATFORM (2026-04-25 — Architecture C).** This file describes the **Architecture B** topology — single shared `statnive.live` zone with outside-Iran hidden-primary NSD on Hetzner + AXFR fan-out to ClouDNS / AT-VPS-B1 / Bunny + defensive parked `.ir`. SamplePlatform's deployment uses **Architecture C** instead (dual-domain, disjoint customer sets per `PLAN.md` §§ Domains / Phase 10 + doc 26 § 3.3a). Under Architecture C:
>
> - **`statnive.live`** is hosted entirely outside Iran — Bunny or Cloudflare DNS only, no Iranian-side NS, no AXFR. Records resolve to Netcup origin. No Iranian resolver ever queries this zone.
> - **`statnive.ir`** is hosted entirely inside Iran — single self-hosted NSD primary on AT-VPS-B1 (no AXFR-in, no hidden-primary, no fan-out). Records resolve to Iranian DC origin. NS delegation at IRNIC: `ns1.statnive.ir` glue → AT-VPS-B1 IP.
> - The `statnive.live` zone file template below (3-NS mix, AXFR source, defensive `.ir` CNAME) is **not used** under Architecture C. Keep it for reference if Architecture B is ever revisited.
> - The CAA + DNSSEC + IDN-bundle items below remain applicable to whichever zone they're attached to.

Canonical DNS configuration for the outside-Iran hidden-primary + ClouDNS AXFR + AT-VPS-B1 Tehran secondary fan-out. Lifted from doc 28 §Gap 2 config samples (lines 466–477).

## Zone file — `statnive.live`

```zone
$TTL 300

; SOA — serial is YYYYMMDDNN (update on every change)
@   IN SOA ns-hidden.statnive.live. hostmaster.statnive.live. (
    2026042001  ; serial
    3600        ; refresh (1h — NSD primary-to-secondary poll)
    600         ; retry  (10m)
    1209600     ; expire (14d — long enough to survive blackouts)
    300         ; negative cache TTL
)

; NS records — mix inside + outside NIN so blackouts don't kill resolution
@   IN NS   ns1.bunny.net.                ; outside anycast
@   IN NS   pns31.cloudns.net.            ; outside AXFR source (ClouDNS Premium)
@   IN NS   ns-tehran.statnive.live.      ; inside NIN — AT-VPS-B1

; Glue — MUST be published at registrar
ns-tehran IN A    185.88.153.10           ; AT-VPS-B1 public v4
ns-tehran IN AAAA 2a02:ec0:300::10        ; AT-VPS-B1 public v6

; CAA — lock issuance to LE + Sectigo; no wildcards
@   IN CAA  0 issue "letsencrypt.org"
@   IN CAA  0 issue "sectigo.com"
@   IN CAA  0 issuewild ";"
@   IN CAA  0 iodef "mailto:secops@statnive.live"

; Defensive — statnive.ir CNAME for users who mistype
www.ir IN CNAME www.statnive.live.
```

## Registrar checklist

- **`.live`** — any mainstream registrar works (Namecheap, Gandi, Cloudflare Registrar — **even though we don't use CF for DNS, the registrar business is OFAC-permissible for the `.live` TLD**).
- **`.ir`** — register at **Pars.ir (IRR)** or **Gandi (€80/yr EUR, EUR billing only, US persons excluded by Gandi T&Cs)**. IRNIC operates `.ir` from nameservers inside NIN — the defensive domain resolves during blackouts even when global `.live` glue is stale.
- **`.ایران`** — Persian IDN. Bundle with `.ir` at IRNIC; same zone content.

## NSD config — AT-VPS-B1 secondary

See [`nsd.conf`](./nsd-config.md) for the full file. Key points:
- `hmac-sha256` TSIG for AXFR-in from Hetzner hidden-primary.
- `chroot: "/etc/nsd"` + `username: nsd` — drops privileges post-bind.
- `hide-version: yes` + `hide-identity: yes` — no fingerprinting surface.
- `allow-notify` + `request-xfr` restrict TSIG-authenticated transfers to 88.99.1.2.
- Companion `nsd-xfr-watch.timer` runs `nsd-control transfer` on boot + hourly — post-blackout convergence happens without waiting for SOA refresh.

## AXFR topology

```
             Hetzner hidden-primary NSD  (ns-hidden.statnive.live)
                         │
                         │  AXFR+TSIG (hmac-sha256, key "statnive-axfr")
                         │
       ┌─────────────────┼─────────────────────────────────┐
       ▼                 ▼                                 ▼
    ClouDNS           AT-VPS-B1 NSD                    Bunny DNS
  (Premium,       (Tehran, inside NIN)              (via CI BIND-file
   AXFR-out                                         import — AXFR-out
   confirmed)                                        UNCONFIRMED)
```

## Post-blackout convergence

If international connectivity drops:
- Iranian eyeballs → AT-VPS-B1 secondary (inside NIN, resolves normally).
- Outside-Iran eyeballs → Bunny + ClouDNS (reachable globally).
- When blackout lifts, Hetzner hidden-primary pushes NOTIFY; `nsd-xfr-watch.timer` on AT-VPS-B1 also pulls AXFR hourly regardless. Convergence in <1 refresh cycle.

## Remaining uncertainties

- **Bunny AXFR-out** — doc 28 line 343 flags as likely-not. Verify via Bunny support ticket before relying on it.
- **ClouDNS pricing** — Premium S ~$3/mo (confirmed); DDoS Protected ~$12.95/mo (also confirmed).
- **`.ایران` IDN registration path** — IRNIC panel, bundled with `.ir`; no separate registrar step needed.