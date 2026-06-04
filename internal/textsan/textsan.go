// Package textsan sanitizes untrusted strings before they enter an LLM's
// context via MCP tool output. Analytics rows carry attacker-controllable
// values (referrer, path, UTM, custom-prop values) that survive ingest
// verbatim — internal/ingest/sanitize.go strips only C0/DEL and the bash
// scripts/skill-sanitizer.sh covers skill files, so neither neutralizes
// the invisible-instruction classes that land in events_raw. This package
// is the MCP's sole stripper and is applied at the single marshalResult
// choke point, recursively over every string in content + structuredContent.
//
// Defense is architectural, not probabilistic (research 78/79): the
// primary defense is that data is returned as JSON *values*, never as free
// narration. This package adds belt-and-suspenders neutralization of the
// highest-severity smuggling vectors (invisible Unicode, HTML comments,
// instruction-marker pseudo-tags) plus redaction of obviously-leaked
// secrets that a tracker value might carry.
package textsan

import (
	"regexp"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// htmlCommentRe matches HTML comments, which are a confirmed LLM-injection
// vector (doc 78). (?s) so it spans newlines; non-greedy to stop at the
// first close.
var htmlCommentRe = regexp.MustCompile(`(?s)<!--.*?-->`)

// instructionTagRe neutralizes the angle-bracket pseudo-tags used to smuggle
// instructions into model context (e.g. the Invariant Labs `<IMPORTANT>`
// Cursor attack). Conservative allow-list of known-dangerous tag names so
// legitimate analytics values aren't mangled.
var instructionTagRe = regexp.MustCompile(`(?is)</?\s*(important|system|instructions?|assistant|tool_call)\b[^>]*>`)

// secretRes detect obviously-leaked credentials that a referrer/UTM value
// might carry (e.g. `?token=sk-…`). Prefixed patterns only — generic
// high-entropy detection is deliberately omitted because it mangles
// legitimate analytics values (hashes, IDs) with high false-positive rate.
var secretRes = []*regexp.Regexp{
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),                                           // AWS access key
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{16,}`),                                      // OpenAI-style key
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}`),                                 // GitHub token
	regexp.MustCompile(`eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`), // JWT
}

const secretMask = "[redacted]"

// isInvisible reports whether r is one of the invisible/bidi/tag runes used
// to hide instructions from human reviewers while remaining LLM-readable
// (Cisco/Robust-Intelligence achieved 100% guardrail evasion with these).
// Code points are written numerically on purpose — embedding the literal
// characters in source would be unreadable and self-defeating.
func isInvisible(r rune) bool {
	switch {
	case r == 0x200B, r == 0x200C, r == 0x200D, r == 0x2060, r == 0xFEFF: // zero-width space/non-joiner/joiner, word-joiner, BOM
		return true
	case r == 0x200E, r == 0x200F: // LRM / RLM
		return true
	case r >= 0x202A && r <= 0x202E: // bidi embeddings (LRE/RLE) + overrides (PDF/LRO/RLO)
		return true
	case r >= 0x2066 && r <= 0x2069: // bidi isolates (LRI/RLI/FSI/PDI)
		return true
	case r >= 0xE0000 && r <= 0xE007F: // Unicode Tag Block
		return true
	default:
		return false
	}
}

// String runs the full neutralization pipeline on one value:
// NFC-normalize → strip invisible runes → strip HTML comments + instruction
// pseudo-tags → redact leaked secrets. Order matters: stripping invisible
// runes first reconnects a secret split by zero-width joiners so the secret
// scan still catches it.
func String(s string) string {
	if s == "" {
		return s
	}

	s = norm.NFC.String(s)
	s = stripInvisible(s)
	s = htmlCommentRe.ReplaceAllString(s, "")
	s = instructionTagRe.ReplaceAllString(s, "")
	s = RedactSecrets(s)

	return s
}

func stripInvisible(s string) string {
	if !strings.ContainsFunc(s, isInvisible) {
		return s
	}

	var b strings.Builder

	b.Grow(len(s))

	for _, r := range s {
		if !isInvisible(r) {
			b.WriteRune(r)
		}
	}

	return b.String()
}

// RedactSecrets masks credential-shaped substrings. Exported so the marshal
// choke point can also scan composed text blocks.
func RedactSecrets(s string) string {
	for _, re := range secretRes {
		s = re.ReplaceAllString(s, secretMask)
	}

	return s
}

// ContainsSecret reports whether s carries a credential-shaped substring
// (used by tests / alerting; redaction is the production action).
func ContainsSecret(s string) bool {
	for _, re := range secretRes {
		if re.MatchString(s) {
			return true
		}
	}

	return false
}

// Clean reports whether s is already free of every class String() would
// alter. Used by the tool-description ASCII guard so the MCP never ships a
// self-poisoning description.
func Clean(s string) bool {
	return s == String(s)
}

// Value recursively sanitizes a JSON-decoded value (string | float64 | bool
// | nil | []any | map[string]any), returning the same shape with every
// string neutralized. Object keys are field names we control, so they pass
// through untouched. This is how the marshalResult choke point cleans an
// entire structuredContent tree in one pass.
func Value(v any) any {
	switch x := v.(type) {
	case string:
		return String(x)
	case []any:
		for i := range x {
			x[i] = Value(x[i])
		}

		return x
	case map[string]any:
		for k, val := range x {
			x[k] = Value(val)
		}

		return x
	default:
		return v
	}
}
