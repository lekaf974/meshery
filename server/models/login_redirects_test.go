package models

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResolvePostLoginRedirect(t *testing.T) {
	t.Parallel()

	const fallback = "/"

	const origin = "https://kanvas.new"

	tests := []struct {
		name     string
		rawRef   string
		origin   string
		expected string
	}{
		{
			name:     "empty ref falls back",
			rawRef:   "",
			expected: fallback,
		},
		{
			name:     "encoded in app path is decoded",
			rawRef:   base64.RawURLEncoding.EncodeToString([]byte("/extension/meshmap")),
			expected: "/extension/meshmap",
		},
		{
			name:     "plain in app path is preserved",
			rawRef:   "/extension/meshmap",
			expected: "/extension/meshmap",
		},
		{
			name:     "relative ref keeps search and hash",
			rawRef:   "/extension/meshmap?mode=design#canvas",
			expected: "/extension/meshmap?mode=design#canvas",
		},
		{
			name:     "encoded absolute url falls back",
			rawRef:   base64.RawURLEncoding.EncodeToString([]byte("https://evil.example/phish")),
			origin:   origin,
			expected: fallback,
		},
		{
			name:     "plain absolute url falls back",
			rawRef:   "https://evil.example/phish",
			origin:   origin,
			expected: fallback,
		},
		{
			name:     "same-origin absolute ref reduces to path and query",
			rawRef:   base64.RawURLEncoding.EncodeToString([]byte("https://kanvas.new/extension/meshmap?mode=design")),
			origin:   origin,
			expected: "/extension/meshmap?mode=design",
		},
		{
			name:     "standard base64 same-origin absolute ref reduces to path and query",
			rawRef:   base64.StdEncoding.EncodeToString([]byte("https://kanvas.new/extension/meshmap?mode=design#canvas")),
			origin:   origin,
			expected: "/extension/meshmap?mode=design",
		},
		{
			name:     "cross-origin absolute ref is rejected",
			rawRef:   base64.RawURLEncoding.EncodeToString([]byte("https://evil.example/extension/meshmap?mode=design")),
			origin:   origin,
			expected: fallback,
		},
		{
			name:     "cross-scheme absolute ref is rejected",
			rawRef:   "http://kanvas.new/extension/meshmap?mode=design",
			origin:   origin,
			expected: fallback,
		},
		{
			name:     "same-origin absolute auth path falls back",
			rawRef:   "https://kanvas.new/user/login?provider=Meshery",
			origin:   origin,
			expected: fallback,
		},
		{
			name:     "invalid base64 falls back",
			rawRef:   "not-base64",
			expected: fallback,
		},
		// Regression coverage: /user/login and /api/user/token are auth
		// initiation paths. Redirecting to them after a successful token
		// exchange re-enters the OAuth dance and caused to hang on
		// the loading splash indefinitely (meshery-server-1345 followed by
		// a second InitiateLogin in the same second).
		{
			name:     "plain /user/login ref falls back",
			rawRef:   "/user/login",
			expected: fallback,
		},
		{
			name:     "/user/login with query falls back",
			rawRef:   "/user/login?provider=Meshery",
			expected: fallback,
		},
		{
			name:     "encoded /user/login ref falls back",
			rawRef:   base64.RawURLEncoding.EncodeToString([]byte("/user/login?provider=Meshery")),
			expected: fallback,
		},
		{
			name:     "plain /api/user/token ref falls back",
			rawRef:   "/api/user/token",
			expected: fallback,
		},
		{
			name:     "/provider ref falls back",
			rawRef:   "/provider?ref=xyz",
			expected: fallback,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			actual := resolvePostLoginRedirect(tc.rawRef, fallback, tc.origin)
			if actual != tc.expected {
				t.Fatalf("expected redirect %q, got %q", tc.expected, actual)
			}
		})
	}
}

func TestSelectPostLoginRefValue(t *testing.T) {
	t.Parallel()

	const cookieName = "playground.meshery.io_ref"
	const cookieValue = "L2V4dGVuc2lvbi9tZXNobWFw" // base64 of /extension/meshmap
	const queryValue = "L2Rhc2hib2FyZA"            // base64 of /dashboard

	tests := []struct {
		name     string
		cookie   *http.Cookie
		query    string
		expected string
	}{
		{
			name:     "cookie value is used when present",
			cookie:   &http.Cookie{Name: cookieName, Value: cookieValue},
			expected: cookieValue,
		},
		// The cookie wins when it is present, including when a provider echoes a
		// different ?ref=. That echo is what landed playground.meshery.io on a
		// 404. When the cookie was never set (Sign In goes straight to the
		// provider), ?ref= is the fallback the comment on selectPostLoginRefValue
		// describes.
		{
			name:     "cookie wins over ?ref= query param",
			cookie:   &http.Cookie{Name: cookieName, Value: cookieValue},
			query:    "?ref=" + queryValue,
			expected: cookieValue,
		},
		{
			name:     "uses ?ref= query param when cookie is missing",
			query:    "?ref=" + queryValue,
			expected: queryValue,
		},
		{
			name:     "ignores ?ref= query param when cookie is empty",
			cookie:   &http.Cookie{Name: cookieName, Value: ""},
			query:    "?ref=" + queryValue,
			expected: "",
		},
		{
			name:     "returns empty when cookie is missing",
			expected: "",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, "/api/user/token"+tc.query, nil)
			if tc.cookie != nil {
				req.AddCookie(tc.cookie)
			}
			actual := selectPostLoginRefValue(req, cookieName)
			if actual != tc.expected {
				t.Fatalf("expected %q, got %q", tc.expected, actual)
			}
		})
	}
}
