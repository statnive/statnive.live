//go:build chatgpt_app

package oauthas

import (
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/google/uuid"

	"github.com/statnive/statnive.live/internal/audit"
	"github.com/statnive/statnive.live/internal/auth"
)

// consentTmpl is the server-rendered consent screen. html/template auto-escapes
// every field, so the (operator-registered) client name and all carried request
// params render inert even if they contain markup. No external assets, no JS —
// air-gap-clean and CSP-trivial.
var consentTmpl = template.Must(template.New("consent").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Connect {{.ClientName}}</title>
<style>
body{font:16px/1.5 system-ui,sans-serif;max-width:34rem;margin:3rem auto;padding:0 1rem;color:#1a1a2e}
.card{border:1px solid #d8d8e0;border-radius:12px;padding:1.5rem}
h1{font-size:1.25rem;margin:0 0 .5rem}
.scope{background:#f4f4f8;border-radius:8px;padding:.75rem 1rem;margin:1rem 0}
fieldset{border:1px solid #e0e0e8;border-radius:8px;margin:1rem 0}
label{display:block;padding:.25rem 0}
.actions{display:flex;gap:.75rem;margin-top:1.25rem}
button{font:inherit;padding:.6rem 1.2rem;border-radius:8px;border:1px solid #c0c0cc;cursor:pointer}
button.primary{background:#2d2d6b;color:#fff;border-color:#2d2d6b}
.muted{color:#666;font-size:.9rem}
</style></head>
<body><div class="card">
<h1>Connect {{.ClientName}}</h1>
<p>{{.ClientName}} is asking to read your statnive analytics through your account.</p>
<div class="scope"><strong>Access:</strong> read-only analytics ({{.Scope}}). It cannot change anything.</div>
<form method="post" action="/consent">
<input type="hidden" name="client_id" value="{{.ClientID}}">
<input type="hidden" name="redirect_uri" value="{{.RedirectURI}}">
<input type="hidden" name="state" value="{{.State}}">
<input type="hidden" name="scope" value="{{.Scope}}">
<input type="hidden" name="response_type" value="code">
<input type="hidden" name="code_challenge" value="{{.CodeChallenge}}">
<input type="hidden" name="code_challenge_method" value="S256">
<input type="hidden" name="resource" value="{{.Audience}}">
{{if .Sites}}
<fieldset><legend>Sites it may read</legend>
{{range .Sites}}<label><input type="checkbox" name="site_ids" value="{{.}}" checked> site {{.}}</label>{{end}}
</fieldset>
<div class="actions">
<button type="submit" name="decision" value="approve" class="primary">Allow</button>
<button type="submit" name="decision" value="deny">Cancel</button>
</div>
{{else}}
<p class="muted">You don't have access to any sites available to this assistant. Ask your operator for a site grant, then try again.</p>
<div class="actions"><button type="submit" name="decision" value="deny">Close</button></div>
{{end}}
</form>
</div></body></html>`))

type consentView struct {
	ClientName    string
	ClientID      string
	RedirectURI   string
	State         string
	Scope         string
	CodeChallenge string
	Audience      string
	Sites         []uint32
}

func (s *Server) renderConsent(w http.ResponseWriter, req authRequest, sites []uint32) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'")

	v := consentView{
		ClientName:    req.client.Name,
		ClientID:      req.client.ID,
		RedirectURI:   req.redirectURI,
		State:         req.state,
		Scope:         req.scope,
		CodeChallenge: req.codeChallenge,
		Audience:      req.audience,
		Sites:         sites,
	}

	if err := consentTmpl.Execute(w, v); err != nil {
		s.logger.Warn("oauthas: render consent", "err", err)
	}
}

// Consent handles POST /consent — the user's allow/deny decision. The mount
// applies sessionMW + requireAuthed, so the user is authenticated. Everything is
// re-validated from the POSTed params (the hidden fields are untrusted
// transport): the client, the exact redirect_uri, PKCE, scope, and resource.
// The decisive security boundary is the server-side site clamp — the issued
// code carries only sites the user genuinely holds within the deployment ceiling,
// so a tampered checkbox list cannot escalate. CSRF is covered by the
// SameSite=Lax session cookie (CLAUDE.md security #6) plus this re-validation.
func (s *Server) Consent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)

		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)

		return
	}

	user := auth.UserFrom(r.Context())
	if user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)

		return
	}

	req, ok := s.validateAuthParams(w, r, r.PostForm.Get)
	if !ok {
		return
	}

	if r.PostFormValue("decision") != "approve" {
		s.audit.Event(r.Context(), audit.EventOAuthConsentDenied,
			slog.String("client_id", req.client.ID),
			slog.String("user_id", user.UserID.String()),
		)
		s.redirectErr(w, r, req.redirectURI, req.state, "access_denied", "user denied the request")

		return
	}

	grants, err := s.sites.LoadUserSites(r.Context(), user.UserID)
	if err != nil {
		s.logger.Warn("oauthas consent: load user sites", "err", err)
		s.redirectErr(w, r, req.redirectURI, req.state, "server_error", "could not load your sites")

		return
	}

	// Server-side clamp: keep only posted sites the user holds AND that are in
	// the deployment ceiling. This is the escalation guard.
	consentable := setOf(s.consentableSites(grants))

	sites := make([]uint32, 0, 4)

	for _, id := range parseSiteIDs(r.PostForm["site_ids"]) {
		if _, ok := consentable[id]; ok {
			sites = append(sites, id)
		}
	}

	if len(sites) == 0 {
		s.redirectErr(w, r, req.redirectURI, req.state, "access_denied", "no authorized sites selected")

		return
	}

	if err := s.issueCode(w, r, req, user.UserID, sites); err != nil {
		s.logger.Warn("oauthas consent: issue code", "err", err)
		s.redirectErr(w, r, req.redirectURI, req.state, "server_error", "could not issue code")
	}
}

// issueCode mints a single-use authorization code for the consented grant and
// redirects back to the client with code + state.
func (s *Server) issueCode(w http.ResponseWriter, r *http.Request, req authRequest, userID uuid.UUID, sites []uint32) error {
	raw, err := newRawToken()
	if err != nil {
		return err
	}

	now := s.now()

	code := AuthCode{
		grant: grant{
			ClientID: req.client.ID,
			UserID:   userID,
			Scope:    req.scope,
			Audience: req.audience,
			SiteIDs:  sites,
		},
		RedirectURI:   req.redirectURI,
		CodeChallenge: req.codeChallenge,
		ExpiresAt:     now.Add(s.cfg.CodeTTL),
	}

	if err := s.store.SaveAuthCode(r.Context(), HashToken(raw), code); err != nil {
		return err
	}

	s.audit.Event(r.Context(), audit.EventOAuthConsentGranted,
		slog.String("client_id", req.client.ID),
		slog.String("user_id", code.UserID.String()),
		slog.Int("site_count", len(sites)),
	)
	s.audit.Event(r.Context(), audit.EventOAuthCodeIssued,
		slog.String("client_id", req.client.ID),
		slog.String("user_id", code.UserID.String()),
	)

	u, err := url.Parse(req.redirectURI)
	if err != nil {
		return err
	}

	qy := u.Query()
	qy.Set("code", raw)

	if req.state != "" {
		qy.Set("state", req.state)
	}

	u.RawQuery = qy.Encode()

	http.Redirect(w, r, u.String(), http.StatusFound)

	return nil
}

func parseSiteIDs(raw []string) []uint32 {
	out := make([]uint32, 0, len(raw))

	for _, s := range raw {
		n, err := strconv.ParseUint(s, 10, 32)
		if err != nil || n == 0 {
			continue
		}

		out = append(out, uint32(n))
	}

	return out
}

func setOf(ids []uint32) map[uint32]struct{} {
	m := make(map[uint32]struct{}, len(ids))
	for _, id := range ids {
		m[id] = struct{}{}
	}

	return m
}
