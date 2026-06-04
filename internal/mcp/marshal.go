package mcp

import (
	"encoding/json"

	"github.com/statnive/statnive.live/internal/textsan"
)

// marshalResult is the SINGLE path from a typed tool result to the bytes
// that enter an MCP client's (and therefore an LLM's) context. It marshals
// the result, re-decodes to a generic tree, and runs textsan.Value over
// every string — neutralizing invisible-Unicode / HTML-comment / instruction
// smuggling and redacting leaked secrets in BOTH the structuredContent tree
// and the text block. No tool handler emits output except through here; a
// test asserts every handler routes through this choke point.
//
// Round-to-10 for rounding consent-modes is a future hook: the dashboard
// does not round read paths today, so v2 returns the store value verbatim
// (the CH-oracle parity test pins this). When the dashboard starts rounding,
// the rounding is applied here, in lockstep.
func marshalResult(result any) (structured any, text string, err error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return nil, "", err
	}

	var tree any
	if err := json.Unmarshal(raw, &tree); err != nil {
		return nil, "", err
	}

	tree = textsan.Value(tree)

	clean, err := json.Marshal(tree)
	if err != nil {
		return nil, "", err
	}

	return tree, string(clean), nil
}
