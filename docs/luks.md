# LUKS disk encryption — operator guide

Operator-facing SOP for the **optional** disk-encryption layer described
in [`docs/rules/security-detail.md`](rules/security-detail.md#rule-9--disk-encryption-luks-optional)
(Rule 9) and CLAUDE.md Security #9. LUKS protects the on-disk data set
(ClickHouse parts, the WAL, the audit log) against offline-device theft
and shared-tenant recovery attacks on cloud VPS disks. It does **not**
replace encrypted backups — that requirement stands regardless of LUKS.

## When to enable

- **Cloud VPS** where the underlying block device is shared with other
  tenants (Hetzner SX/AX series, Asiatech shared pool, ParsPack VPS).
  The cloud operator controls the physical device; LUKS plus a
  passphrase held by the statnive operator keeps the data opaque even
  under host-side imaging or reallocation.
- **Laptop / workstation** where the disk physically leaves the office.

## When to skip

- **Dedicated cage hardware** with physical access controls and a
  working encrypted-backup pipeline — the threat model is already
  covered and the 40–50% I/O hit is not worth paying.
- **Iranian DC deployments** where the operator has physical cage
  access (Asiatech Milad Tower, ParsPack, Shatel) and encrypted
  `clickhouse-backup` archives are shipped off-box per
  [`docs/runbook.md`](runbook.md#backup--restore-phase-7b2).

## I/O cost

Measured on ext4 over LUKS1 with `aes-xts-plain64 --key-size 512 --hash sha256`
— the write path takes a **40–50% hit** (doc: `rules/security-detail.md`
Rule 9). At the 7 K EPS design ingest rate this translates to a 3–5 K
EPS effective ceiling on the encrypted spindle. If the deployment is
close to the ceiling already, either skip LUKS or upgrade the host's
CPU (AES-NI + AVX2 halve the overhead).

Size the host accordingly. `cryptsetup benchmark` on the target host
is the right number to plan against, not the manual's figures.

## One-time setup

Assumes a dedicated data disk `/dev/sdb` for ClickHouse data + WAL +
audit. Do **not** LUKS-encrypt the root disk on a production host
unless you have a working unattended-unlock recipe — operators can't
type passphrases through a blackout.

```bash
# 1. Format the block device as LUKS2. The passphrase you enter here is
#    one of two authentication factors — see "Key custody" below.
sudo cryptsetup luksFormat /dev/sdb \
  --type luks2 \
  --cipher aes-xts-plain64 \
  --key-size 512 \
  --hash sha256 \
  --pbkdf argon2id \
  --iter-time 2000

# 2. Open the device under a stable mapper name. The name is referenced
#    from crypttab, fstab, and systemd unit dependencies below.
sudo cryptsetup open /dev/sdb statnive-data

# 3. Filesystem + mount point. ext4 is the default for ClickHouse;
#    xfs is also supported — pick whichever matches your fleet.
sudo mkfs.ext4 /dev/mapper/statnive-data
sudo install -d -o statnive -g statnive -m 0750 /var/lib/statnive-live
sudo mount /dev/mapper/statnive-data /var/lib/statnive-live
```

Verify: `cryptsetup status statnive-data` reports `cipher: aes-xts-plain64`
and `type: LUKS2`.

## Persist across reboots

### `/etc/crypttab`

```
statnive-data  /dev/disk/by-uuid/<UUID-of-sdb>  none  luks,discard
```

`<UUID-of-sdb>` comes from `blkid /dev/sdb`. The `none` keyfile entry
means systemd prompts at boot — **manual reboots only**. For unattended
reboot, see the clevis/TPM section below.

### `/etc/fstab`

```
/dev/mapper/statnive-data  /var/lib/statnive-live  ext4  defaults,nofail  0 2
```

`nofail` is intentional — if LUKS unlock fails, the host still boots
and can be fixed via console; without it the system drops into rescue
mode, which is hard to hit on an Iranian DC bare-metal box.

## Key custody

| Credential | Location | Purpose |
|---|---|---|
| **Primary passphrase** | 1Password (operator team vault), entry `statnive-live/luks/<host>` | Day-to-day unlock. Rotate quarterly. |
| **Recovery key** | Physical printout in a tamper-evident envelope, off-premises (legal team safe) | Last-resort unlock. Used once = rotate. |

Add the recovery key with `cryptsetup luksAddKey /dev/sdb`. Add a new
primary and remove the old one with `cryptsetup luksChangeKey /dev/sdb`.

**Never store the LUKS passphrase in the same system as the encrypted
backups' `age` private key** — a single compromise must not unlock
both the live disk and the off-site archive.

## Boot-time unlock

**v1 default: operator-typed at console.** An operator has console
access (IPMI / out-of-band on bare metal, cloud console on VPS) and
types the passphrase after every boot. A five-minute reboot window is
acceptable for statnive-live — the tracker's WAL buffers ingest during
the brief downtime, and dashboard queries can tolerate the gap.

**v1.1 option: clevis + TPM unattended unlock.** For hosts that need
to reboot unattended (Iranian DC during sanctions-hour maintenance
windows), `clevis-luks-bind` seals the LUKS key to the TPM's PCR state:

```bash
sudo clevis luks bind -d /dev/sdb tpm2 '{"pcr_ids":"7"}'
```

TPM unlock is **opt-in** and out of scope for Phase 2c. When enabled,
sealing to `PCR 7` (Secure Boot state) trades passphrase custody for
firmware-integrity trust. Document the choice in the host's runbook
entry and remove the operator passphrase slot only after two months
of stable unattended boots.

## Verifying the stack is wired correctly

```bash
# The LUKS layer is mounted and ClickHouse writes go to it:
sudo lsblk -o NAME,FSTYPE,TYPE,MOUNTPOINT /dev/sdb
# Expect: sdb -> crypto_LUKS, sdb/statnive-data -> ext4, /var/lib/statnive-live

# A writable probe as the service user:
sudo -u statnive touch /var/lib/statnive-live/.luks-probe && \
  sudo -u statnive rm /var/lib/statnive-live/.luks-probe

# Benchmark the overhead:
sudo cryptsetup benchmark --cipher aes-xts-plain64 --key-size 512
```

## Backup drill — still required

Encrypted backups are **mandatory even with LUKS enabled** — LUKS only
protects against offline theft of the live disk. See
[`docs/runbook.md`](runbook.md#backup--restore-phase-7b2) for the SOP
and [CLAUDE.md Security #8](../CLAUDE.md#security-14-features-all-v1)
for the threat-model reasoning. LUKS does not change the drill cadence.
