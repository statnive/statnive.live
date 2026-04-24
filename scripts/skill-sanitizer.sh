#!/usr/bin/env bash
# skill-sanitizer.sh — F6 supply-chain guard on skill content.
#
# Research backing: doc 25 line 69 endorses meta-security skills that
# scan other agent skills for malicious content. This is a lightweight
# bash + perl equivalent tuned to the statnive-live skill surface.
# Scans for:
#   - Unicode Tag Block (U+E0000–E007F) — the canonical LLM-prompt-
#     injection steganographic vector.
#   - Zero-width joiners / selectors (U+200B–U+200D, U+FEFF).
#   - Bidi overrides (U+202A–U+202E, U+2066–U+2069) — trojan-source
#     complement to the `bidichk` Go linter (which only covers *.go).
#
# HTML comments are deliberately NOT scanned: legitimate markdown uses
# them heavily for code-example labels, section markers, and TOC
# control, and any prompt injection hidden there is visible under
# `view source` — far less stealthy than a Tag Block codepoint.
#
# Perl (not grep) so the regex works the same way on macOS BSD grep,
# Linux GNU grep, and ugrep-wrapper environments.
#
# Scope: .claude/skills/**/*.md + .claude/skills/**/*.yml +
# .claude/skills/**/*.yaml + docs/**/*.md. Exit non-zero on any hit
# with file:line:codepoint so CI reports actionable context.
#
# Pass `--selftest <fixture-dir>` to run against a fixture directory
# instead of the project tree — used by `make skill-sanitizer-selftest`
# to keep the rule honest.

set -euo pipefail

usage() {
	cat >&2 <<-EOF
		Usage:
		  skill-sanitizer.sh                  # scan project .claude/skills + docs
		  skill-sanitizer.sh --selftest DIR   # scan a fixture directory
	EOF
	exit 2
}

scan_dir() {
	local dir="$1"
	local had_hit=0

	# Collect target files (null-delimited to tolerate spaces).
	local files=()
	while IFS= read -r -d '' f; do
		files+=("$f")
	done < <(find "$dir" \( -name '*.md' -o -name '*.yml' -o -name '*.yaml' \) -type f -print0 2>/dev/null)

	if [ ${#files[@]} -eq 0 ]; then
		return 0
	fi

	# One perl invocation for every target file. $. is per-file because
	# perl resets the line counter when ARGV rolls to the next file;
	# close ARGV explicitly at eof to make that reset deterministic.
	if perl -CSDA -ne '
		BEGIN { $hit = 0 }
		if (/([\x{200B}-\x{200D}\x{FEFF}\x{202A}-\x{202E}\x{2066}-\x{2069}\x{E0000}-\x{E007F}])/) {
			printf "%s:%d: hidden codepoint U+%04X\n", $ARGV, $., ord($1);
			$hit = 1;
		}
		close ARGV if eof;
		END { exit($hit) }
	' "${files[@]}"; then
		:
	else
		had_hit=1
	fi

	return $had_hit
}

main() {
	if [ "${1:-}" = "--selftest" ]; then
		if [ -z "${2:-}" ]; then
			usage
		fi

		# Transparently relay scan_dir's exit code: 0 = clean, 1 = hits.
		# The caller (make skill-sanitizer-selftest) verifies the expected
		# code per-fixture-dir.
		if scan_dir "$2"; then
			exit 0
		fi
		exit 1
	fi

	if [ $# -gt 0 ]; then
		usage
	fi

	local root
	root="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"

	local hit=0
	local dir

	for dir in "$root/.claude/skills" "$root/docs"; do
		if [ ! -d "$dir" ]; then
			continue
		fi

		if ! scan_dir "$dir"; then
			hit=1
		fi
	done

	if [ $hit -ne 0 ]; then
		echo "skill-sanitizer: one or more files contain hidden Unicode / bidi content — reject" >&2
		exit 1
	fi

	echo "skill-sanitizer: clean"
}

main "$@"
