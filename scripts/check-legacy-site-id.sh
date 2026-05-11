#!/usr/bin/env bash
# check-legacy-site-id.sh — CI gate for the v0.0.9 per-site-admin
# scaffolding. Catches the bug class where new dashboard/admin code
# reads the legacy `users.site_id` column instead of going through
# auth.SitesStore.
#
# After migration 010 the canonical authorization source is the
# user_sites grant table; users.site_id stays for one release for
# rollback safety. Any new SQL that SELECTs site_id directly from the
# users table is wrong — at best it returns one site of many, at worst
# it lets a now-revoked grant leak through.
#
# Allowed locations:
#   - internal/storage/migrations/    : SQL DDL is allowed to reference it
#   - test/admin_user_sites_e2e_test.go : the test that backfills user_sites from users.site_id
#   - internal/auth/store.go          : the legacy ListUsers / GetUserByEmail paths
#   - internal/auth/bootstrap.go      : first-run admin creation (single user, single site)
#
# Run from the repo root.

set -euo pipefail

# SQL gate: literal "users.site_id" SQL reference.
# grep for the SQL column reference, drop comment-only lines (Go //,
# SQL --) so narrative docstrings explaining the legacy column don't
# trip the gate.
HITS=$(grep -rEn 'users\.site_id' \
    --include='*.go' --include='*.sql' \
    cmd/ internal/ test/ scripts/ 2>/dev/null \
    | grep -vE '/(migrations/[0-9]+_.*\.sql|auth/(store|bootstrap|handlers|types)\.go|admin/(users|sites|goals)_handlers\.go|admin/users_handlers_test\.go|admin/sites_handlers_test\.go|admin/sites_currency_tz_test\.go|storage/migrations.go|storage/migrate\.go|admin/testutil_test\.go):' \
    | grep -vE '^test/admin_user_sites_e2e_test\.go:' \
    | grep -vE ':[0-9]+:\s*(//|--)' \
    || true)

if [ -n "$HITS" ]; then
    echo "FAIL: new code references the legacy users.site_id column."
    echo "      Use auth.SitesStore.LoadUserSites / actor.Sites instead."
    echo ""
    echo "Offending lines:"
    echo "$HITS"
    exit 1
fi

# Comment+narrative occurrences are fine; the gate is silent.
echo "OK: no new users.site_id SQL references."
