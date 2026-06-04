package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/statnive/statnive.live/internal/auth"
	"github.com/statnive/statnive.live/internal/storage"
)

func TestServeStdio_RoundTrip(t *testing.T) {
	t.Parallel()

	s := newTestServer(&fakeStore{overview: &storage.OverviewResult{Visitors: 42}})

	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`, // notification → no reply
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"overview","arguments":{"site":"1","range":"7d"}}}`,
		``,          // blank line → skipped
		`{bad json`, // → parse error
	}, "\n")

	var out strings.Builder

	if err := s.ServeStdio(context.Background(), strings.NewReader(in), &out, wildcardActor()); err != nil {
		t.Fatalf("ServeStdio: %v", err)
	}

	// Expect 4 response lines: initialize, tools/list, tools/call, parse-error.
	// The notification and the blank line produce no output.
	var lines []string

	sc := bufio.NewScanner(strings.NewReader(out.String()))
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) != "" {
			lines = append(lines, sc.Text())
		}
	}

	if len(lines) != 4 {
		t.Fatalf("got %d response lines, want 4:\n%s", len(lines), out.String())
	}

	// Last line must be the parse error with null id.
	var perr response
	if err := json.Unmarshal([]byte(lines[3]), &perr); err != nil {
		t.Fatalf("decode parse-error line: %v", err)
	}

	if perr.Error == nil || perr.Error.Code != codeParseError {
		t.Errorf("last line should be parse error, got %+v", perr.Error)
	}

	// id 2 line is tools/list with 3 tools.
	var listResp struct {
		Result struct {
			Tools []listedTool `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &listResp); err != nil {
		t.Fatalf("decode tools/list line: %v", err)
	}

	if len(listResp.Result.Tools) != 3 {
		t.Errorf("tools/list returned %d tools", len(listResp.Result.Tools))
	}
}

func httpServer(t *testing.T, actor *auth.User) *httptest.Server {
	t.Helper()

	s := newTestServer(&fakeStore{overview: &storage.OverviewResult{Visitors: 7}})
	h := s.HTTPHandler(HTTPOptions{})

	// Inject the actor into the request context (the real chain does this via
	// auth middleware).
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(auth.WithSession(r.Context(), actor, nil)))
	})

	return httptest.NewServer(wrapped)
}

func TestHTTP_PostHappyPath(t *testing.T) {
	t.Parallel()

	ts := httpServer(t, wildcardActor())
	defer ts.Close()

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"overview","arguments":{"site":"1","range":"7d"}}}`

	resp, err := http.Post(ts.URL+"/mcp", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type = %q", ct)
	}
}

func TestHTTP_NotificationReturns202(t *testing.T) {
	t.Parallel()

	ts := httpServer(t, wildcardActor())
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/mcp", "application/json",
		strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("notification status = %d, want 202", resp.StatusCode)
	}
}

func TestHTTP_GetIs405(t *testing.T) {
	t.Parallel()

	ts := httpServer(t, wildcardActor())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/mcp")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET status = %d, want 405", resp.StatusCode)
	}
}

func TestHTTP_ForbiddenOrigin(t *testing.T) {
	t.Parallel()

	ts := httpServer(t, wildcardActor())
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	req.Header.Set("Origin", "https://evil.example.com")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("evil origin status = %d, want 403", resp.StatusCode)
	}
}

func TestHTTP_UnsupportedProtocolVersion(t *testing.T) {
	t.Parallel()

	ts := httpServer(t, wildcardActor())
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	req.Header.Set("MCP-Protocol-Version", "1999-01-01")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad protocol version status = %d, want 400", resp.StatusCode)
	}
}

func TestHTTP_Unauthenticated(t *testing.T) {
	t.Parallel()

	// No actor in context → tools/call should be a JSON-RPC -32600.
	ts := httpServer(t, nil)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/mcp", "application/json",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"overview","arguments":{"site":"1"}}}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()

	var r response
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if r.Error == nil || r.Error.Code != codeInvalidRequest {
		t.Errorf("unauthenticated should be -32600, got %+v", r.Error)
	}
}
