// Package mcp implements statnive-live's read-only Model Context Protocol
// server — the v2 "agent surface". It exposes the existing read-only
// storage.Store (plus a few operator/admin reads) as deterministic MCP
// tools over stdio (air-gap-safe, default) and opt-in loopback HTTP. There
// is NO LLM, no model, no inference, and no new SQL in this package: every
// tool is a thin adapter over an already-tested read path, and the client
// (Claude / ChatGPT / any MCP host) supplies all the intelligence.
//
// jsonrpc.go holds the transport-agnostic JSON-RPC 2.0 envelope types and
// the protocol error codes. The stdio and HTTP transports both decode into
// these types and hand them to Server.Dispatch.
package mcp

import "encoding/json"

// jsonRPCVersion is the only protocol version this server speaks on the
// JSON-RPC envelope (distinct from the MCP-Protocol-Version negotiated in
// initialize).
const jsonRPCVersion = "2.0"

// JSON-RPC 2.0 standard error codes (https://www.jsonrpc.org/specification).
// We use -32602 (invalid params) for every application-level rejection an
// LLM can provoke — unknown tool, bad args, bad range, AND cross-tenant
// access — so a denied request is an explicit error, never a silent empty
// result. Tool *execution* failures (CH down, not-yet-implemented) are NOT
// JSON-RPC errors: they come back as a successful tools/call result with
// isError=true (see result.go), which is what MCP clients expect.
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

// request is an inbound JSON-RPC message. ID is kept as RawMessage so it
// round-trips byte-for-byte (the spec allows string, number, or null and
// requires the response to echo it exactly). A message with no ID is a
// notification — it gets no response (and, over HTTP, a 202).
type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// isNotification reports whether the message carries no ID (JSON-RPC
// notification — fire-and-forget, no response expected).
func (r request) isNotification() bool {
	return len(r.ID) == 0
}

// response is an outbound JSON-RPC message. Exactly one of Result / Error
// is set. ID echoes the request ID verbatim.
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError is the JSON-RPC error object.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *rpcError) Error() string { return e.Message }

// newResultResponse builds a success response, marshaling result into the
// Result field. A marshal failure degrades to an internal error so the
// transport always has a well-formed response to send.
func newResultResponse(id json.RawMessage, result any) response {
	raw, err := json.Marshal(result)
	if err != nil {
		return newErrorResponse(id, codeInternalError, "failed to encode result")
	}

	return response{JSONRPC: jsonRPCVersion, ID: id, Result: raw}
}

// newErrorResponse builds an error response with the given code + message.
func newErrorResponse(id json.RawMessage, code int, message string) response {
	return response{
		JSONRPC: jsonRPCVersion,
		ID:      id,
		Error:   &rpcError{Code: code, Message: message},
	}
}

// invalidParams is the shorthand used throughout dispatch for the umbrella
// -32602 rejection (unknown tool, bad args, bad range, cross-tenant).
func invalidParams(id json.RawMessage, message string) response {
	return newErrorResponse(id, codeInvalidParams, message)
}
