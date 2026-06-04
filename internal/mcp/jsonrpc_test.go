package mcp

import (
	"encoding/json"
	"testing"
)

func TestRequest_NotificationDetection(t *testing.T) {
	t.Parallel()

	var notif request
	if err := json.Unmarshal([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`), &notif); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if !notif.isNotification() {
		t.Error("message without id should be a notification")
	}

	var req request
	if err := json.Unmarshal([]byte(`{"jsonrpc":"2.0","id":7,"method":"tools/list"}`), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if req.isNotification() {
		t.Error("message with id should not be a notification")
	}
}

func TestResponse_EchoesIDVerbatim(t *testing.T) {
	t.Parallel()

	// JSON-RPC ids may be numbers or strings; both must echo byte-for-byte.
	for _, id := range []string{`7`, `"abc"`, `null`} {
		resp := newResultResponse(json.RawMessage(id), map[string]any{"ok": true})

		out, err := json.Marshal(resp)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}

		var decoded struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Result  json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(out, &decoded); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}

		if decoded.JSONRPC != "2.0" {
			t.Errorf("jsonrpc = %q, want 2.0", decoded.JSONRPC)
		}

		if string(decoded.ID) != id {
			t.Errorf("id = %s, want %s", decoded.ID, id)
		}
	}
}

func TestErrorResponse_ShapeAndCodes(t *testing.T) {
	t.Parallel()

	resp := invalidParams(json.RawMessage(`1`), "not authorized for site")

	if resp.Error == nil || resp.Error.Code != codeInvalidParams {
		t.Fatalf("want invalid-params error, got %+v", resp.Error)
	}

	if resp.Result != nil {
		t.Error("error response must not carry a result")
	}

	out, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// A success result field must be absent on an error response.
	var m map[string]json.RawMessage
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if _, ok := m["result"]; ok {
		t.Error("error response serialized a result field")
	}

	if _, ok := m["error"]; !ok {
		t.Error("error response missing error field")
	}
}

func TestErrorCodesAreStandard(t *testing.T) {
	t.Parallel()

	want := map[string]int{
		"parse":    -32700,
		"request":  -32600,
		"method":   -32601,
		"params":   -32602,
		"internal": -32603,
	}

	got := map[string]int{
		"parse":    codeParseError,
		"request":  codeInvalidRequest,
		"method":   codeMethodNotFound,
		"params":   codeInvalidParams,
		"internal": codeInternalError,
	}

	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s code = %d, want %d", k, got[k], v)
		}
	}
}
