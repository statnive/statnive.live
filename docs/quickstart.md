# Quickstart — fresh install → first event in five minutes

Target: you have Docker + Git + an unused localhost port. In five minutes
you'll have a running statnive-live instance, a tracker snippet pasted
into one of your pages, and the first event visible on the dashboard.

All commands run from the repo root after a `git clone`.

## Prerequisites

- Docker 20.10+ (for the dev ClickHouse container).
- Go 1.24+ if you're building from source. Pre-built binaries are on the
  releases page; this guide uses `make build` so the same path works in
  both cases.
- `openssl` for the master secret.

## Minute 1 — Clone + master secret

```bash
git clone https://github.com/statnive/statnive.live.git
cd statnive.live
openssl rand -hex 32 > master.key
chmod 0600 master.key
```

The binary refuses to start if `master.key` is world-readable — that 0600
is load-bearing.

## Minute 2 — Boot ClickHouse

```bash
docker compose -f deploy/docker-compose.dev.yml up -d --wait clickhouse
```

Takes ~5 seconds. The container listens on `127.0.0.1:19000` (native) +
`127.0.0.1:18123` (HTTP) and persists data to a named Docker volume.

## Minute 3 — Build + boot statnive-live

```bash
make build

STATNIVE_MASTER_SECRET_PATH=./master.key \
STATNIVE_CLICKHOUSE_ADDR=127.0.0.1:19000 \
STATNIVE_DASHBOARD_SPA_ENABLED=true \
STATNIVE_DEV=1 \
STATNIVE_AUTH_SESSION_SECURE=false \
STATNIVE_BOOTSTRAP_ADMIN_EMAIL=you@example.com \
STATNIVE_BOOTSTRAP_ADMIN_PASSWORD='change-me-now' \
  ./bin/statnive-live -c config/statnive-live.yaml
```

- `STATNIVE_DEV=1` + `STATNIVE_AUTH_SESSION_SECURE=false` let the session
  cookie flow over HTTP for local development. Production deployments
  use TLS and drop both flags.
- `STATNIVE_BOOTSTRAP_ADMIN_*` seed your first admin user on the first
  boot. Unsetting them on later boots is fine — the bootstrap is
  idempotent.

In a new terminal, confirm the binary is up:

```bash
curl -fsS http://127.0.0.1:8080/healthz
```

Expect a JSON body with `"status":"ok"`.

## Minute 4 — Add a site

Open <http://127.0.0.1:8080/app> in your browser. Log in with the
bootstrap credentials you just set.

In the top-right site switcher you'll see **no sites yet — add one**.
Click it (or navigate to **Admin → Sites** directly). In the "Add site"
form:

- **Hostname** — the domain you'll paste the tracker on, e.g.
  `example.com` or `localhost` if you're testing against a local page.
- **Slug** — leave blank to let statnive-live auto-generate one from the
  hostname.
- **Timezone** — `Asia/Tehran` by default; override if your operator
  locale differs.

Click **Add site**. A row appears in the table with the tracker snippet
pre-rendered.

## Minute 5 — Paste the snippet + visit

Copy the `<script>…</script>` block shown in the Sites table (triple-
click the `<pre>` to select, `Cmd+C` / `Ctrl+C`). It looks like this:

```html
<script src="http://127.0.0.1:8080/tracker.js" async defer></script>
```

Paste into the `<head>` of any HTML file served by the hostname you
registered, then open that page in a browser. The tracker POSTs its
first event to `/api/event` within a few hundred milliseconds.

Back in the dashboard, switch to **Overview** and wait up to 10 seconds
for the realtime visitor count to tick. Your first event has arrived.

## Troubleshooting

- **Overview shows `0 visitors` after several page loads.** The tracker
  reads `window.location.hostname` — make sure the hostname you visited
  matches the one you registered exactly (`example.com` ≠ `www.example.com`).
- **`/api/event` requests return 204 with no increment.** Silent drop
  on unknown hostname (intentional — keeps bots from fishing site IDs).
  Check **Admin → Sites** shows the host you're serving the page on.
- **`STATNIVE_MASTER_SECRET not provided` on boot.** The binary wants
  either `STATNIVE_MASTER_SECRET` (the hex bytes directly) or
  `STATNIVE_MASTER_SECRET_PATH` pointing at a 0600-mode file.
- **Browser blocks the tracker request.** Statnive-live respects
  `DNT: 1` and `Sec-GPC: 1` — if your browser sends them, the tracker
  short-circuits before the network call. Turn the header off for
  testing, or use a private window without those protections.
- **Session cookie missing after login.** Add `STATNIVE_DEV=1` and
  `STATNIVE_AUTH_SESSION_SECURE=false` to the boot env. Without TLS
  the browser refuses `SameSite=Lax; Secure` cookies over HTTP.

## Next steps

- [`docs/runbook.md`](runbook.md) covers production deployment,
  backup + restore, TLS rotation, and the air-gap verification gate.
- [`docs/luks.md`](luks.md) walks through optional disk-encryption for
  shared-tenant VPS deployments.
- [`deploy/systemd/README.md`](../deploy/systemd/README.md) has the
  hardened systemd unit + install recipe.
