#!/usr/bin/env bash
# PostToolUse(Edit|Write) hook: regenerate the OpenAPI contract after a change
# to the router, the overlay/components, or a documented response struct.
# Best-effort and NON-FATAL — never blocks the edit (always exits 0), and is a
# no-op when the Go toolchain or redocly is unavailable. CI `spec-check` is the
# binding gate; this just keeps the working tree fresh for fast feedback.
set -uo pipefail

fp="$(cat | python3 -c 'import sys,json
try:
    d=json.load(sys.stdin)
    print((d.get("tool_input") or {}).get("file_path","") or "")
except Exception:
    print("")' 2>/dev/null || true)"

[ -z "$fp" ] && exit 0

case "$fp" in
  *api/overlay.yaml|*api/components/*|*internal/httpapi/router.go|*internal/dashboard/router.go|*internal/admin/router.go|*internal/dashboard/mcp_tokens.go|*internal/storage/result.go|*internal/ingest/event.go|*cmd/statnive-live/main.go)
    if command -v go >/dev/null 2>&1; then
      ( cd "${CLAUDE_PROJECT_DIR:-.}" && go run ./cmd/specgen >/dev/null 2>&1 || true )
      echo "rebuild-spec: regenerated api/openapi.yaml (run 'make spec-lint' before commit)" >&2
    fi
    ;;
esac

exit 0
