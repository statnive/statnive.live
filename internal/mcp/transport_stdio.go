package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"

	"github.com/statnive/statnive.live/internal/auth"
)

// maxStdioLine bounds a single JSON-RPC line on stdin. Requests are tiny;
// this only guards against an unbounded read.
const maxStdioLine = 1 << 20 // 1 MiB

// ServeStdio runs the newline-delimited JSON-RPC loop over r/w until EOF or
// ctx cancellation (MCP stdio framing: one JSON object per line, no embedded
// newlines). actor is the synthetic operator bound to the whole session
// (fail-closed unless the operator passed --allow-sites / --all-sites). All
// diagnostics must go to stderr by the caller — w here is the JSON-RPC
// channel and must carry nothing but responses.
func (s *Server) ServeStdio(ctx context.Context, r io.Reader, w io.Writer, actor *auth.User) error {
	enc := json.NewEncoder(w) // Encode appends '\n' → newline framing for free

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), maxStdioLine)

	for scanner.Scan() {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			if encErr := enc.Encode(newErrorResponse(json.RawMessage("null"), codeParseError, "parse error")); encErr != nil {
				return encErr
			}

			continue
		}

		resp := s.Handle(ctx, req, actor)
		if resp == nil {
			continue // notification — no reply
		}

		if err := enc.Encode(*resp); err != nil {
			return err
		}
	}

	return scanner.Err()
}
