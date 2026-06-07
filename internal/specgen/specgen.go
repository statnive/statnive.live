// Package specgen derives the OpenAPI route skeleton from the live chi router.
//
// It builds the router via httpapi.BuildRouter in SpecMode (all conditional
// groups forced on, all deps stubbed) and walks it with chi.Walk, so the
// generated contract surface can never drift from the registered routes. The
// emitter is deterministic by construction (sorted paths/methods, no maps, no
// timestamps) so the committed artifact diffs cleanly.
//
// Output is a *skeleton* — paths + methods + a deterministic operationId + a
// tag, and NO responses. An operation without responses is invalid OpenAPI, so
// a brand-new route fails lint until the overlay enriches it: that's the
// "go document me" signal. The hand-authored overlay (api/overlay.yaml) is
// deep-merged over this skeleton (see Merge) to produce api/openapi.yaml.
package specgen

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	yaml "gopkg.in/yaml.v3"

	"github.com/statnive/statnive.live/internal/httpapi"
)

// nopHandler is a 200 handler; nopMW is a pass-through middleware. Stub deps
// never dial/embed, so SpecRouter is pure wiring.
func nopHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
}

func nopMW(h http.Handler) http.Handler { return h }

// StubDeps returns an all-stub SpecMode RouterDeps: no-op middlewares/handlers,
// SpecMode=true so every conditional group mounts. Shared by cmd/specgen and
// the httpapi coverage tests so both walk the identical route table.
func StubDeps() httpapi.RouterDeps {
	h := nopHandler()

	return httpapi.RouterDeps{
		CORS: nopMW, RateLimit: nopMW, LoginRateLimit: nopMW, McpTokenRateLimit: nopMW,
		Session: nopMW, APIToken: nopMW, RequireAuthed: nopMW, RequireCSRF: nopMW,
		FastReject: nopMW, Backpressure: nopMW,
		Ingest: h, AuthLogin: h, AuthLogout: h, AuthMe: h,
		Health: h, Metrics: h, About: h, LIA: h, DPA: h, Tracker: h, Landing: h, Favicon: h,
		PrivacyPolicy: h, PrivacyPage: h, PrivacyOptOut: h, PrivacyAccess: h, PrivacyErase: h, PrivacyConsent: h,
		Spa:          h,
		MountOAuthAS: func(chi.Router) error { return nil },
		SpecMode:     true,
	}
}

// SpecRouter builds the full-surface SpecMode router.
func SpecRouter() (*chi.Mux, error) { return httpapi.BuildRouter(StubDeps()) }

// Route is a single documented operation.
type Route struct {
	Method string
	Path   string
}

// Excluded reports routes deliberately kept out of the contract: the SPA shell
// (/app + /app/*, which chi fans out across all 9 HTTP methods).
func Excluded(_, path string) bool {
	return path == "/app" || strings.HasPrefix(path, "/app/")
}

// Routes walks the SpecMode router and returns the sorted, deduped, non-excluded
// documented route set.
func Routes() ([]Route, error) {
	mux, err := SpecRouter()
	if err != nil {
		return nil, err
	}

	seen := map[string]Route{}

	walkErr := chi.Walk(mux, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if Excluded(method, route) {
			return nil
		}

		seen[method+" "+route] = Route{Method: method, Path: route}

		return nil
	})
	if walkErr != nil {
		return nil, walkErr
	}

	out := make([]Route, 0, len(seen))
	for _, r := range seen {
		out = append(out, r)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}

		return out[i].Method < out[j].Method
	})

	return out, nil
}

// OperationID derives a stable camelCase id from METHOD + path. Path params
// ({id}, {lang}) and separators (., -, _) are folded into PascalCase segments.
// The id is provisional — the overlay's hand-authored operationId wins on merge.
func OperationID(method, path string) string {
	var b strings.Builder
	b.WriteString(strings.ToLower(method))

	segs := strings.Split(strings.Trim(path, "/"), "/")
	wrote := false

	for _, seg := range segs {
		seg = strings.Trim(seg, "{}")
		if seg == "" {
			continue
		}

		seg = strings.NewReplacer(".", " ", "-", " ", "_", " ").Replace(seg)
		for _, w := range strings.Fields(seg) {
			b.WriteString(strings.ToUpper(w[:1]) + w[1:])

			wrote = true
		}
	}

	if !wrote {
		b.WriteString("Root") // "/" → getRoot
	}

	return b.String()
}

// TagFor maps a path to its PascalCase-plural tag. Provisional (overlay wins).
func TagFor(path string) string {
	switch {
	case path == "/api/event":
		return "Ingestion"
	case path == "/api/login", path == "/api/logout", path == "/api/user":
		return "Auth"
	case strings.HasPrefix(path, "/api/admin/"):
		return "Admin"
	case strings.HasPrefix(path, "/api/mcp/"):
		return "McpTokens"
	case strings.HasPrefix(path, "/api/privacy/"):
		return "Privacy"
	case strings.HasPrefix(path, "/api/stats/"),
		path == "/api/realtime/visitors", path == "/api/props/list",
		path == "/api/goals/list", path == "/api/sites":
		return "Stats"
	case path == "/healthz", path == "/metrics", path == "/api/about":
		return "Ops"
	case path == "/privacy", strings.HasPrefix(path, "/legal/"):
		return "Legal"
	default:
		return "Public" // "/", "/favicon.ico", "/tracker.js"
	}
}

// methodKeyword maps an HTTP method to its lowercase OpenAPI path-item key.
func methodKeyword(method string) string { return strings.ToLower(method) }

// Skeleton renders the deterministic OpenAPI 3.1 skeleton (paths/methods/
// operationId/tags, NO responses) as YAML bytes. Built as a string by sorted
// iteration — no map marshalling — so output is byte-stable run to run.
func Skeleton(routes []Route) []byte {
	// Group methods per path, preserving sorted order from Routes().
	type pathItem struct {
		path    string
		methods []Route
	}

	var items []pathItem

	idx := map[string]int{}
	for _, r := range routes {
		i, ok := idx[r.Path]
		if !ok {
			idx[r.Path] = len(items)
			items = append(items, pathItem{path: r.Path})
			i = idx[r.Path]
		}

		items[i].methods = append(items[i].methods, r)
	}

	var b strings.Builder
	b.WriteString("# GENERATED by cmd/specgen from the chi router — DO NOT EDIT.\n")
	b.WriteString("# Skeleton only (no responses). Semantics live in api/overlay.yaml;\n")
	b.WriteString("# the merged contract is api/openapi.yaml (cmd/specgen deep-merge).\n")
	b.WriteString("openapi: 3.1.0\n")
	b.WriteString("info:\n")
	b.WriteString("  title: statnive-live API\n")
	b.WriteString("  version: 0.0.0-gen\n")
	b.WriteString("paths:\n")

	for _, it := range items {
		b.WriteString("  ")
		b.WriteString(yamlKey(it.path))
		b.WriteString(":\n")

		for _, r := range it.methods {
			b.WriteString("    ")
			b.WriteString(methodKeyword(r.Method))
			b.WriteString(":\n")
			b.WriteString("      operationId: ")
			b.WriteString(OperationID(r.Method, r.Path))
			b.WriteString("\n")
			b.WriteString("      tags:\n")
			b.WriteString("        - ")
			b.WriteString(TagFor(r.Path))
			b.WriteString("\n")
		}
	}

	return []byte(b.String())
}

// yamlKey quotes a path used as a YAML mapping key. Paths contain `/` and `{}`
// which are safe unquoted in block style, but quoting keeps the emitter robust.
func yamlKey(s string) string { return "\"" + strings.ReplaceAll(s, "\"", "\\\"") + "\"" }

// Merge deep-merges the hand-authored overlay over the generated skeleton and
// returns the merged OpenAPI document as YAML. The overlay is a plain partial
// OpenAPI doc (info/servers/tags/security/components + per-operation semantics);
// the skeleton supplies the openapi version + every path/method/operationId/tag.
// Merge semantics (Overlay 1.0-aligned): mappings recurse; scalars and
// SEQUENCES are replaced wholesale by the overlay (so a per-op parameters array
// or tags list in the overlay wins). Output is deterministic — skeleton key
// order is preserved, overlay-only keys append in the overlay's order.
func Merge(skeleton, overlay []byte) ([]byte, error) {
	var skNode, ovNode yaml.Node
	if err := yaml.Unmarshal(skeleton, &skNode); err != nil {
		return nil, fmt.Errorf("parse skeleton: %w", err)
	}

	if err := yaml.Unmarshal(overlay, &ovNode); err != nil {
		return nil, fmt.Errorf("parse overlay: %w", err)
	}

	skRoot := docRoot(&skNode)
	ovRoot := docRoot(&ovNode)

	if skRoot == nil {
		return nil, errors.New("skeleton has no root mapping")
	}

	if ovRoot == nil { // empty overlay → skeleton verbatim
		return yaml.Marshal(skRoot)
	}

	merged := mergeNode(skRoot, ovRoot)

	out := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{merged}}

	var b strings.Builder

	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)

	if err := enc.Encode(out); err != nil {
		return nil, fmt.Errorf("encode merged: %w", err)
	}

	_ = enc.Close()

	return []byte(b.String()), nil
}

// docRoot unwraps a DocumentNode to its root content node.
func docRoot(n *yaml.Node) *yaml.Node {
	if n.Kind == yaml.DocumentNode && len(n.Content) == 1 {
		return n.Content[0]
	}

	return n
}

// mergeNode deep-merges over onto base. Only same-kind mappings recurse;
// everything else is replaced by over.
func mergeNode(base, over *yaml.Node) *yaml.Node {
	if base.Kind != yaml.MappingNode || over.Kind != yaml.MappingNode {
		return over
	}

	// Index base keys.
	idx := map[string]int{}
	for i := 0; i < len(base.Content); i += 2 {
		idx[base.Content[i].Value] = i
	}

	// Start from a shallow copy of base's pairs.
	out := &yaml.Node{Kind: yaml.MappingNode, Tag: base.Tag, Style: base.Style}
	out.Content = append(out.Content, base.Content...)

	for i := 0; i < len(over.Content); i += 2 {
		k := over.Content[i]

		v := over.Content[i+1]
		if bi, ok := idx[k.Value]; ok {
			out.Content[bi+1] = mergeNode(out.Content[bi+1], v)
		} else {
			out.Content = append(out.Content, k, v)
		}
	}

	return out
}
