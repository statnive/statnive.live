package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/statnive/statnive.live/internal/auth"
)

// defaultMaxHTTPBody bounds a single MCP HTTP request body.
const defaultMaxHTTPBody = 64 << 10 // 64 KiB

// supportedProtocolVersions are the MCP spec revisions this server accepts in
// the MCP-Protocol-Version header. An absent header is allowed (pre-init /
// lenient client); a present-but-unknown value is rejected with 400, per the
// 2025-11-25 spec (verified via Context7).
var supportedProtocolVersions = map[string]bool{
	"2025-03-26": true,
	"2025-06-18": true,
	"2025-11-25": true,
}

// HTTPOptions configures the inbound HTTP transport. AllowedOrigins augments
// the always-allowed loopback set (used by the v2.5 chatgpt-app profile);
// for the v2 loopback profile it is empty.
type HTTPOptions struct {
	MaxBody        int64
	AllowedOrigins []string
}

// HTTPHandler returns the http.Handler for the single Streamable-HTTP
// endpoint (POST /mcp). The auth middleware chain (rate-limit → session →
// api-token → require-auth → role/grant) runs BEFORE this handler and leaves
// the actor in the request context; this handler reads it via auth.UserFrom.
// It never dials out — purely inbound (air-gap-safe).
func (s *Server) HTTPHandler(opts HTTPOptions) http.Handler {
	maxBody := opts.MaxBody
	if maxBody <= 0 {
		maxBody = defaultMaxHTTPBody
	}

	allowed := opts.AllowedOrigins

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Streamable HTTP server→client GET stream is unsupported (this
		// server is stateless request/response). 405 keeps the contract
		// explicit and reserves GET for a future session mode.
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)

			return
		}

		// DNS-rebinding guard (MCP-Inspector RCE CVE-2025-49596 class).
		if !originAllowed(r, allowed) {
			http.Error(w, "forbidden origin", http.StatusForbidden)

			return
		}

		if !protocolVersionOK(r) {
			http.Error(w, "unsupported MCP-Protocol-Version", http.StatusBadRequest)

			return
		}

		data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
		if err != nil {
			writeJSON(w, http.StatusOK, newErrorResponse(json.RawMessage("null"), codeParseError, "parse error"))

			return
		}

		var req request
		if err := json.Unmarshal(data, &req); err != nil {
			writeJSON(w, http.StatusOK, newErrorResponse(json.RawMessage("null"), codeParseError, "parse error"))

			return
		}

		resp := s.Handle(r.Context(), req, auth.UserFrom(r.Context()))
		if resp == nil {
			// JSON-RPC notification — Streamable HTTP returns 202, no body.
			w.WriteHeader(http.StatusAccepted)

			return
		}

		writeJSON(w, http.StatusOK, *resp)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// originAllowed permits an absent Origin (non-browser MCP clients send none)
// and any loopback origin; otherwise the origin (or its host) must be in the
// configured allow-list.
func originAllowed(r *http.Request, allowed []string) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}

	u, err := url.Parse(o)
	if err != nil {
		return false
	}

	if isLoopbackHost(u.Hostname()) {
		return true
	}

	for _, a := range allowed {
		if strings.EqualFold(a, o) || strings.EqualFold(a, u.Hostname()) {
			return true
		}
	}

	return false
}

func isLoopbackHost(h string) bool {
	switch h {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

func protocolVersionOK(r *http.Request) bool {
	v := r.Header.Get("MCP-Protocol-Version")
	if v == "" {
		return true
	}

	return supportedProtocolVersions[v]
}
