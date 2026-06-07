#!/usr/bin/env bash
# PreToolUse(Edit|Write) hook: block hand-edits to GENERATED contract artifacts.
# Exit 2 blocks the tool call and feeds the message back to the model. The
# hand-authored sources — api/overlay.yaml and api/components/** — are
# explicitly ALLOWED; only the regenerated outputs are protected.
#
# Local convenience guard only. The binding gate is CI `make spec-check`.
set -euo pipefail

fp="$(cat | python3 -c 'import sys,json
try:
    d=json.load(sys.stdin)
    print((d.get("tool_input") or {}).get("file_path","") or "")
except Exception:
    print("")' 2>/dev/null || true)"

[ -z "$fp" ] && exit 0

case "$fp" in
  *api/openapi.yaml|*api/openapi.gen.yaml|*web/src/api/generated.ts|*/clients/ts/*|clients/ts/*)
    echo "Refusing to edit a GENERATED contract artifact: $fp" >&2
    echo "Edit api/overlay.yaml (or the Go struct), then run 'make spec-build' / 'npm --prefix web run types:gen'." >&2
    exit 2
    ;;
esac

exit 0
