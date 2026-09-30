package models

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"

	"github.com/meshery/meshery/server/core"
)

func resolvePostLoginRedirect(rawRef, fallback, origin string) string {
	if rawRef == "" {
		return fallback
	}

	if decoded, ok := decodePostLoginRef(rawRef); ok {
		if target, ok := safePostLoginTarget(decoded, origin); ok {
			return target
		}
	}

	if target, ok := safePostLoginTarget(rawRef, origin); ok {
		return target
	}

	return fallback
}

// decodePostLoginRef accepts the server's raw-url encoding and the standard
// base64 produced by btoa in the Sign In link. A failed decode is not fatal:
// callers also try the raw value as a plaintext path.
func decodePostLoginRef(rawRef string) (string, bool) {
	if decoded, err := core.DecodeRefURL(rawRef); err == nil {
		return decoded, true
	}
	decoded, err := base64.StdEncoding.DecodeString(rawRef)
	if err != nil {
		return "", false
	}
	return string(decoded), true
}

// selectPostLoginRefValue returns the raw (encoded or plaintext) value to
// feed into resolvePostLoginRedirect when the auth flow returns to
// TokenHandler.
// Meshery is the source of truth for its own post-login destination: the value
// is captured into a cookie at InitiateLogin time and read back here. The
// ?ref= query param is a fallback for callers that never went through
// InitiateLogin (mesheryctl, direct extension callbacks) and for older
// provider deployments that still echo a ref back to us. We deliberately do
// not try to merge the two — when the cookie is present it wins outright,
// since stale provider-side state (e.g. a synthesized ref baked into Hydra
// state during a custom-domain bounce) was the bug this routing change was
// introduced to fix.
func selectPostLoginRefValue(r *http.Request, cookieName string) string {
	if r == nil {
		return ""
	}
	if ck, err := r.Cookie(cookieName); err == nil {
		return ck.Value
	}
	if r.URL == nil {
		return ""
	}
	return r.URL.Query().Get("ref")
}

// postLoginOrigin is the origin a same-origin absolute ref is compared
// against. The configured public server URL wins over the inbound Host,
// which a proxy or client can rewrite.
func postLoginOrigin(r *http.Request) string {
	if r == nil {
		return ""
	}
	if raw, ok := r.Context().Value(MesheryServerURL).(string); ok {
		if origin := strings.TrimSpace(raw); origin != "" {
			return origin
		}
	}
	if r.Host == "" {
		return ""
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// authInitiationPaths are server routes whose job is to *start* authentication.
// Post-login redirects must never land on one of these, otherwise the browser
// immediately re-enters the OAuth dance and the original target is lost. The
// intermittent not-loading behavior was reproduced as exactly this:
// TokenHandler succeeded and then redirected to /user/login?provider=Meshery,
// which restarted InitiateLogin mid-mount.
var authInitiationPaths = []string{
	"/user/login",
	"/auth/login",
	"/api/user/token",
	"/provider",
}

// safePostLoginTarget validates a ref and returns the in-app path to redirect
// to. Relative refs are kept as-is (path, query, and hash). A same-origin
// absolute ref is reduced to its path and query so an older client that sent
// window.location.href still lands on the design page. Every cross-origin ref
// is rejected.
func safePostLoginTarget(rawURL, origin string) (string, bool) {
	if rawURL == "" || strings.HasPrefix(rawURL, "//") {
		return "", false
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", false
	}

	target := rawURL
	if parsed.Scheme != "" || parsed.Host != "" {
		if !sameOrigin(parsed, origin) {
			return "", false
		}
		target = parsed.RequestURI()
		if target == "" {
			target = "/"
		}
	} else if !strings.HasPrefix(rawURL, "/") {
		return "", false
	}

	if !isAllowedAppPath(parsed.Path) {
		return "", false
	}

	return target, true
}

func isAllowedAppPath(path string) bool {
	for _, p := range authInitiationPaths {
		if path == p || strings.HasPrefix(path, p+"/") {
			return false
		}
	}
	return true
}

func sameOrigin(ref *url.URL, origin string) bool {
	if ref == nil || ref.Hostname() == "" || origin == "" {
		return false
	}
	base, err := url.Parse(origin)
	if err != nil || base.Hostname() == "" {
		return false
	}
	if !strings.EqualFold(ref.Hostname(), base.Hostname()) {
		return false
	}
	return portOrDefault(ref) == portOrDefault(base)
}

func portOrDefault(u *url.URL) string {
	if u == nil {
		return ""
	}
	if p := u.Port(); p != "" {
		return p
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return "443"
	case "http":
		return "80"
	default:
		return ""
	}
}
