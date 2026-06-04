package mcp

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/statnive/statnive.live/internal/storage"
)

// TestHTTP_SSE_OneShot — a client that sends Accept: text/event-stream gets the
// JSON-RPC response back as a single SSE event (the ChatGPT-app shape), while
// the default JSON path is unaffected (covered elsewhere).
func TestHTTP_SSE_OneShot(t *testing.T) {
	t.Parallel()

	ts := httpServer(t, wildcardActor())
	defer ts.Close()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, ts.URL+"/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Accept", "application/json, text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}

	defer func() { _ = resp.Body.Close() }()

	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}

	body, _ := io.ReadAll(resp.Body)
	s := string(body)

	if !strings.HasPrefix(s, "event: message\ndata: ") {
		t.Fatalf("not an SSE frame: %q", s)
	}

	// The data line must carry a valid JSON-RPC result.
	data := strings.TrimSuffix(strings.TrimPrefix(s, "event: message\ndata: "), "\n\n")

	var rpc map[string]any
	if err := json.Unmarshal([]byte(data), &rpc); err != nil {
		t.Fatalf("SSE data not valid JSON: %v\n%q", err, data)
	}

	if _, ok := rpc["result"]; !ok {
		t.Errorf("SSE data missing result: %v", rpc)
	}
}

func oauthTestServer(t *testing.T, scopes []string) *Server {
	t.Helper()

	return New(Config{
		Store:       &fakeStore{overview: &storage.OverviewResult{}},
		Registry:    newTestRegistry(),
		Version:     "t",
		GeoEnabled:  true,
		OAuthScopes: scopes,
		Budget:      BudgetConfig{CallsPerMin: 100, WildcardFactor: 1},
		Now:         func() time.Time { return testNow },
	})
}

// toolsListMeta returns each tool's _meta from a tools/list response.
func toolsListMeta(t *testing.T, s *Server) map[string]map[string]any {
	t.Helper()

	resp := call(t, s, wildcardActor(), "tools/list", nil)

	var got struct {
		Tools []struct {
			Name string         `json:"name"`
			Meta map[string]any `json:"_meta"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &got); err != nil {
		t.Fatalf("decode tools/list: %v", err)
	}

	out := make(map[string]map[string]any, len(got.Tools))
	for _, tl := range got.Tools {
		out[tl.Name] = tl.Meta
	}

	return out
}

// TestToolsList_SecuritySchemes_WhenOAuth — with OAuthScopes set (chatgpt-app
// profile), every tool advertises _meta.securitySchemes (noauth + oauth2) so
// ChatGPT knows to run the auth-code+PKCE flow.
func TestToolsList_SecuritySchemes_WhenOAuth(t *testing.T) {
	t.Parallel()

	meta := toolsListMeta(t, oauthTestServer(t, []string{"analytics:read"}))

	for name, m := range meta {
		if m == nil {
			t.Errorf("%s: missing _meta when OAuth on", name)

			continue
		}

		schemes, ok := m["securitySchemes"].([]any)
		if !ok || len(schemes) != 2 {
			t.Errorf("%s: securitySchemes = %v, want [noauth, oauth2]", name, m["securitySchemes"])
		}
	}
}

// TestToolsList_NoMeta_WithoutOAuth — the v2 loopback/stdio surface stays
// byte-identical: no OAuthScopes + no widgets ⇒ no _meta on any tool.
func TestToolsList_NoMeta_WithoutOAuth(t *testing.T) {
	t.Parallel()

	meta := toolsListMeta(t, oauthTestServer(t, nil))

	for name, m := range meta {
		if m != nil {
			t.Errorf("%s: _meta should be omitted without OAuth/widgets, got %v", name, m)
		}
	}
}
