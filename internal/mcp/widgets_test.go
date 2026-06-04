package mcp

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/statnive/statnive.live/internal/storage"
)

func widgetsServer(t *testing.T, enabled bool) *Server {
	t.Helper()

	return New(Config{
		Store:          &fakeStore{overview: &storage.OverviewResult{}},
		Registry:       newTestRegistry(),
		Version:        "t",
		GeoEnabled:     true,
		WidgetsEnabled: enabled,
		Budget:         BudgetConfig{CallsPerMin: 100, WildcardFactor: 1},
		Now:            func() time.Time { return testNow },
	})
}

func initCapabilities(t *testing.T, s *Server) map[string]any {
	t.Helper()

	resp := call(t, s, nil, "initialize", nil)

	var got struct {
		Capabilities map[string]any `json:"capabilities"`
	}
	if err := json.Unmarshal(resp.Result, &got); err != nil {
		t.Fatalf("decode initialize: %v", err)
	}

	return got.Capabilities
}

// TestWidgets_DisabledByDefault — the v2 surface: no resources capability, no
// resources, no per-tool _meta.ui.
func TestWidgets_DisabledByDefault(t *testing.T) {
	t.Parallel()

	s := widgetsServer(t, false)

	if _, ok := initCapabilities(t, s)["resources"]; ok {
		t.Error("resources capability advertised when widgets disabled")
	}

	resp := call(t, s, wildcardActor(), "resources/list", nil)

	var list struct {
		Resources []any `json:"resources"`
	}

	_ = json.Unmarshal(resp.Result, &list)

	if len(list.Resources) != 0 {
		t.Errorf("resources/list = %d, want 0 when disabled", len(list.Resources))
	}

	// resources/read must refuse when disabled.
	rd := call(t, s, wildcardActor(), "resources/read", map[string]any{"uri": widgetURI})
	if rd.Error == nil {
		t.Error("resources/read should error when widgets disabled")
	}

	// overview tool must NOT carry _meta.ui.
	if meta := toolsListMeta(t, s)["overview"]; meta != nil {
		t.Errorf("overview _meta should be nil when widgets disabled, got %v", meta)
	}
}

// TestWidgets_EnabledServesWidget — widgets on: capability advertised, the
// ui:// resource is listed + readable, and the top tools carry _meta.ui.
func TestWidgets_EnabledServesWidget(t *testing.T) {
	t.Parallel()

	s := widgetsServer(t, true)

	if _, ok := initCapabilities(t, s)["resources"]; !ok {
		t.Error("resources capability not advertised when widgets enabled")
	}

	// resources/list contains the widget.
	resp := call(t, s, wildcardActor(), "resources/list", nil)

	var list struct {
		Resources []struct {
			URI      string `json:"uri"`
			MimeType string `json:"mimeType"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(resp.Result, &list); err != nil {
		t.Fatalf("decode resources/list: %v", err)
	}

	if len(list.Resources) != 1 || list.Resources[0].URI != widgetURI {
		t.Fatalf("resources/list = %+v, want one %s", list.Resources, widgetURI)
	}

	// resources/read returns the embedded HTML.
	rd := mustResourceText(t, call(t, s, wildcardActor(), "resources/read", map[string]any{"uri": widgetURI}))
	if !strings.Contains(rd, "<!doctype html>") || !strings.Contains(rd, "window.openai") {
		t.Errorf("widget HTML not served correctly: %.80q", rd)
	}

	// Unknown resource → error.
	bad := call(t, s, wildcardActor(), "resources/read", map[string]any{"uri": "ui://widget/nope.html"})
	if bad.Error == nil {
		t.Error("resources/read of unknown uri should error")
	}

	// overview tool carries _meta.ui.resourceUri == widgetURI.
	meta := toolsListMeta(t, s)["overview"]
	if meta == nil {
		t.Fatal("overview _meta missing when widgets enabled")
	}

	ui, _ := meta["ui"].(map[string]any)
	if ui == nil || ui["resourceUri"] != widgetURI {
		t.Errorf("overview _meta.ui.resourceUri = %v, want %s", meta["ui"], widgetURI)
	}
}

func mustResourceText(t *testing.T, resp *response) string {
	t.Helper()

	if resp.Error != nil {
		t.Fatalf("resources/read error: %+v", resp.Error)
	}

	var got struct {
		Contents []struct {
			Text string `json:"text"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(resp.Result, &got); err != nil {
		t.Fatalf("decode resources/read: %v", err)
	}

	if len(got.Contents) != 1 {
		t.Fatalf("contents = %d, want 1", len(got.Contents))
	}

	return got.Contents[0].Text
}
