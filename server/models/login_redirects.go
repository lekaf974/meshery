package models

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/meshery/meshery/server/core"
)

func resolvePostLoginRedirect(rawRef, fallback, host string) string {
	if rawRef == "" {
		return fallback
	}

	if decoded, ok := decodePostLoginRef(rawRef); ok {
		if target, ok := safePostLoginTarget(decoded, host); ok {
			return target
		}
	}

	if target, ok := safePostLoginTarget(rawRef, host); ok {
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
// is captured into a cookie at InitiateLogin time and read back here. A cookie
// that carries a destination wins outright over a ?ref= the provider echoes
// back, since stale provider-side state (e.g. a synthesized ref baked into
// Hydra state during a custom-domain bounce) was the bug this routing change
// was introduced to fix. A cookie whose value is empty carries no destination
// and is therefore absent: ?ref= is used instead, which is how a Sign In taken
// from a page reached during an anonymous session keeps its query.
func selectPostLoginRefValue(r *http.Request, cookieName string) string {
	if ck, err := r.Cookie(cookieName); err == nil && ck.Value != "" {
		return ck.Value
	}
	return r.URL.Query().Get("ref")
}

// postLoginHost is the host an absolute ref is compared against. The
// configured public server URL wins over the inbound Host, which a proxy or
// client can rewrite.
func postLoginHost(r *http.Request) string {
	if raw, ok := r.Context().Value(MesheryServerURL).(string); ok {
		if configured, err := url.Parse(strings.TrimSpace(raw)); err == nil && configured.Hostname() != "" {
			return configured.Hostname()
		}
	}
	return (&url.URL{Host: r.Host}).Hostname()
}

// authInitiationPaths are routes whose job is to *start* authentication.
// Post-login redirects must never land on one of these, otherwise the browser
// immediately re-enters the OAuth dance and the original target is lost. The
// intermittent not-loading behavior was reproduced as exactly this:
// TokenHandler succeeded and then redirected to /user/login?provider=Meshery,
// which restarted InitiateLogin mid-mount.
// "/login" is the remote provider's own login page, not a Meshery route at all:
// a custom-domain bounce synthesizes it into the ref it echoes back, and honoring
// it served the catch-all handler as a 404 at playground.meshery.io/login.
var authInitiationPaths = []string{
	"/login",
	"/user/login",
	"/auth/login",
	"/api/user/token",
	"/provider",
}

// safePostLoginTarget validates a ref and returns the in-app path to redirect
// to. A backslash is rejected outright: browsers resolve it as an authority
// delimiter, so "/\evil.example" would leave the origin. The auth-path denylist
// runs against the cleaned path, because http.Redirect itself cleans the
// Location it writes, so "/../user/login" would otherwise reach /user/login.
// Relative refs are kept as-is (path, query, and hash). An absolute ref on
// Meshery's own host is reduced to its path and query so an older client that
// sent window.location.href still lands on the design page. The scheme is not
// part of that comparison: the callback URL carries a hard-coded http:// on
// every deployment that leaves MESHERY_SERVER_CALLBACK_URL unset, so an https
// page would otherwise fail to match its own host. Every ref on another host
// is rejected.
func safePostLoginTarget(rawURL, host string) (string, bool) {
	if rawURL == "" || strings.HasPrefix(rawURL, "//") || strings.Contains(rawURL, `\`) {
		return "", false
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", false
	}

	target := rawURL
	if parsed.Scheme != "" || parsed.Host != "" {
		if !sameHost(parsed, host) {
			return "", false
		}
		target = parsed.RequestURI()
	} else if !strings.HasPrefix(rawURL, "/") {
		return "", false
	}

	if !isAllowedAppPath(path.Clean(parsed.Path)) {
		return "", false
	}

	return target, true
}

func isAllowedAppPath(appPath string) bool {
	for _, p := range authInitiationPaths {
		if appPath == p || strings.HasPrefix(appPath, p+"/") {
			return false
		}
	}
	return true
}

func sameHost(ref *url.URL, host string) bool {
	return host != "" && ref.Hostname() != "" && strings.EqualFold(ref.Hostname(), host)
}
