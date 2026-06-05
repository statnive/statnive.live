package mcp

import _ "embed"

// widget.html is the v3 ChatGPT-app UI layer — a self-contained, zero-outbound
// generic renderer embedded into the binary (no CDN, air-gap-safe). Served as
// an MCP resource at widgetURI when mcp.widgets.enabled is true.
//
//go:embed widget.html
var widgetHTML string

const (
	// widgetURI is the canonical ui:// resource the top tools point their
	// _meta.ui.resourceUri at. One generic widget backs every tool (it renders
	// whatever structuredContent it receives); per-tool ECharts widgets are an
	// increment on the same contract.
	widgetURI = "ui://widget/statnive.html"

	// widgetMIME is the OpenAI Apps-SDK widget media type.
	widgetMIME = "text/html+skybridge"

	widgetName = "statnive analytics widget"
)

// defaultWidget is the descriptor attached to a tool's catalog row so tools/list
// can emit _meta.ui (only when mcp.widgets.enabled — see metaMap). Kept here so
// the URI is defined once alongside the resource that serves it.
func defaultWidget() *widgetMeta {
	return &widgetMeta{
		TemplateURI: widgetURI,
		Invoking:    "Loading analytics…",
		Invoked:     "Analytics ready",
		Accessible:  true,
	}
}

// resourcesList implements MCP resources/list. Empty unless widgets are
// enabled, so the v2 surface advertises nothing.
func (s *Server) resourcesList(req request) *response {
	resources := []map[string]any{}

	if s.widgetsEnabled {
		resources = append(resources, map[string]any{
			"uri":      widgetURI,
			"name":     widgetName,
			"mimeType": widgetMIME,
		})
	}

	return ptr(newResultResponse(req.ID, map[string]any{"resources": resources}))
}

// resourcesRead implements MCP resources/read for the widget URI.
func (s *Server) resourcesRead(req request) *response {
	var args struct {
		URI string `json:"uri"`
	}
	if err := decodeStrict(req.Params, &args); err != nil {
		return ptr(invalidParams(req.ID, "invalid resources/read params"))
	}

	if !s.widgetsEnabled || args.URI != widgetURI {
		return ptr(invalidParams(req.ID, "unknown resource: "+args.URI))
	}

	return ptr(newResultResponse(req.ID, map[string]any{
		"contents": []map[string]any{{
			"uri":      widgetURI,
			"mimeType": widgetMIME,
			"text":     widgetHTML,
		}},
	}))
}
