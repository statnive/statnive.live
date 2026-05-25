package sites

import (
	"net/url"
	"strings"
)

// wwwBareToggleHost returns the host with its "www." prefix toggled and
// reports whether the toggle produced a different value. Used by
// LookupSitePolicy (ingest path, hostname-from-body) and indirectly by
// OriginIndex.Lookup (CORS path, Origin-header) so the two layers agree
// on www. equivalence.
//
//	"foo.com"           -> ("www.foo.com", true)
//	"www.foo.com"       -> ("foo.com", true)
//	"www.www.foo.com"   -> ("www.foo.com", true)   strips one level only
//	""                  -> ("", false)
//
// Single-level toggle by design: mirrors the existing
// LookupSitePolicy retry semantic. Multi-www. spoofing (an attacker
// registering www.www.foo.com and expecting www.foo.com fallthrough)
// must be an explicit allowlist entry, not a recursive shortcut.
func wwwBareToggleHost(host string) (string, bool) {
	if host == "" {
		return "", false
	}

	if strings.HasPrefix(host, "www.") {
		return strings.TrimPrefix(host, "www."), true
	}

	return "www." + host, true
}

// wwwBareToggleOrigin returns the origin with its host's "www." prefix
// toggled, preserving scheme and port. Returns ("", false) when the
// origin is malformed, missing a scheme, or missing a host.
//
//	"https://foo.com"          -> ("https://www.foo.com", true)
//	"https://www.foo.com"      -> ("https://foo.com", true)
//	"https://foo.com:8443"     -> ("https://www.foo.com:8443", true)
//	"https://www.foo.com:8443" -> ("https://foo.com:8443", true)
//
// Operates on already-normalized origins (lowercase scheme + host, no
// path / query / fragment). Callers should pass NormalizeOrigin output
// or browser-canonical Origin header values.
func wwwBareToggleOrigin(origin string) (string, bool) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", false
	}

	altHost, ok := wwwBareToggleHost(u.Hostname())
	if !ok {
		return "", false
	}

	if port := u.Port(); port != "" {
		return u.Scheme + "://" + altHost + ":" + port, true
	}

	return u.Scheme + "://" + altHost, true
}
