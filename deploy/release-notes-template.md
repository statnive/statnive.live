# ${VERSION}

Signed airgap bundle attached. Verify with `airgap-verify-bundle.sh` against the public key at `/etc/statnive/release-key.pub`.

## Artifacts

| File | Purpose | SHA-256 |
| --- | --- | --- |
| `statnive-live-${VERSION}-linux-amd64-airgap.tar.gz` | Airgap bundle (binary + SPA + scripts) | `${SHA256_BUNDLE}` |
| `SHA256SUMS` | Sums for every artifact in the bundle | (signed by `SHA256SUMS.sig`) |
| `SHA256SUMS.sig` | Ed25519 signature of `SHA256SUMS` | — |

## Install by deployment posture

The same binary supports three deployment topologies. Pick the posture that matches your target environment.

### SaaS (statnive.live multi-tenant)

```bash
make release-customer POSTURE=saas VERSION=${VERSION} \
    HOST=root@your-saas-host
```

No license JWT required. The systemd drop-in records the posture for `/healthz` + audit.

### Customer VPS outside Iran (single-tenant, license-gated)

```bash
make release-customer POSTURE=outside-iran VERSION=${VERSION} \
    HOST=root@customer-host \
    LICENSE=/path/to/customer.license.jwt
```

Customer-issued Ed25519 license JWT required. Standard outbound allowed (ACME / package updates / Telegram).

### Customer VPS inside Iran (Asiatech, air-gap)

```bash
make release-customer POSTURE=inside-iran VERSION=${VERSION} \
    HOST=root@customer-asiatech-host \
    LICENSE=/path/to/customer.license.jwt \
    CERT_DIR=/path/with/fullchain.pem+privkey.pem
```

Implies `--apply-iptables` + Asiatech NTP profile. TLS PEMs must be pre-issued on the outside-Iran `cert-forge` and supplied via `CERT_DIR` (no ACME-from-Iran per CLAUDE.md anti-patterns). Customer-issued license JWT required.

## Verifying the signature

```bash
# Once per host (operator stages the release pubkey)
sudo install -m 0644 deploy/keys/release-signing.pub /etc/statnive/release-key.pub

# On every release tarball
./deploy/airgap-verify-bundle.sh \
    statnive-live-${VERSION}-linux-amd64-airgap.tar.gz \
    /etc/statnive/release-key.pub
```

A mismatched signature aborts the install — never silently accepted.

## Posture reference

| Posture | License | Outbound | NTP | TLS source |
| --- | --- | --- | --- | --- |
| `saas` | none | unrestricted | distro default | manual rotation or ACME via reverse proxy |
| `outside-iran` | required | unrestricted | distro default | manual rotation or ACME |
| `inside-iran` | required | `iptables -P OUTPUT DROP` + allowlist | `chrony.conf.asiatech` (time.asiatech.ir et al.) | rsync from outside-Iran cert-forge bastion |

See `docs/runbook.md` § Phase 10 for the full cutover playbook.
