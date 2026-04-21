#!/usr/bin/env bash
#
# ops/token-budget.sh — AI-surface token-budget gate.
#
# Asserts that the project's AI-facing markdown stays within the line-count
# and description-length caps documented in docs/tooling.md. Backs the
# token-optimization pass per research docs 08 / 18 / 62 / 75.
#
# Run from the repo root. Exit 0 on pass, 1 on any cap exceeded.
#
# Wire-in points:
#   - `make audit` (already chained from pre-commit gate)
#   - `make audit` is what `/simplify` re-runs after a doc change
#
# Caps:
#   - CLAUDE.md      <= 220 lines (root routing index)
#   - PLAN.md        <= 550 lines (phase plan + verification)
#   - docs/tooling.md <= 320 lines (skill routing detail)
#   - Each custom skill SKILL.md `description:` field <= 1100 chars
#     (~150 tokens; one trigger sentence + file globs + enforcement summary)

set -e

cd "$(dirname "$0")/.."

fail=0

check_lines() {
    local f="$1" cap="$2" n
    if [ ! -f "$f" ]; then
        echo "MISSING: $f"
        fail=1
        return
    fi
    n=$(wc -l < "$f" | tr -d ' ')
    if [ "$n" -gt "$cap" ]; then
        echo "FAIL: $f has $n lines, cap $cap"
        fail=1
    fi
}

check_lines CLAUDE.md 220
check_lines PLAN.md 550
check_lines docs/tooling.md 320

CUSTOM_SKILLS=(
    tenancy-choke-point-enforcer
    air-gap-validator
    clickhouse-rollup-correctness
    clickhouse-cluster-migration
    preact-signals-bundle-budget
    blake3-hmac-identity-review
    wal-durability-review
    ratelimit-tuning-review
    gdpr-code-review
    dsar-completeness-checker
    iranian-dc-deploy
    geoip-pipeline-review
    clickhouse-operations-review
    clickhouse-upgrade-playbook
)

for s in "${CUSTOM_SKILLS[@]}"; do
    f=".claude/skills/$s/SKILL.md"
    if [ ! -f "$f" ]; then
        echo "MISSING: $f"
        fail=1
        continue
    fi
    desc=$(awk '
        /^---$/ { if (in_fm) exit; in_fm = 1; next }
        in_fm && /^description:/ { capture = 1 }
        capture && /^[a-z_-]+:/ && !/^description:/ { capture = 0 }
        capture { print }
    ' "$f")
    chars=$(printf %s "$desc" | wc -c | tr -d ' ')
    if [ "$chars" -gt 1100 ]; then
        echo "FAIL: $f description is $chars chars, cap 1100"
        fail=1
    fi
done

if [ "$fail" -eq 0 ]; then
    echo "ok: token budgets respected"
fi
exit $fail
