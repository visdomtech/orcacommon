// Package litespaserver serves a CDN-hosted single-page app: it resolves a
// live frontend version, fetches the SPA's index.html (and an allow-list of
// static files) from a CDN, injects a per-request CSP nonce, and serves the
// result. It is a generic, reusable module with no dependency on any specific
// application's configuration package.
package litespaserver

// Config captures all caller-specific values needed to serve a CDN-hosted SPA.
// The caller resolves environment-specific defaults (prod vs dev) before
// constructing Config, keeping the module environment-agnostic.
type Config struct {
	// CDNPrefix is the CDN base URL, e.g. "https://hc-cdn.doublefin.com".
	CDNPrefix string

	// CDNVersion locks the served version when non-empty, bypassing the
	// database entirely. Useful for local development or pinning a release.
	CDNVersion string

	// StaticPaths is the allow-list of static file paths (e.g.
	// "/unsubscribed.html") served via the CDN proxy alongside index.html.
	StaticPaths []string

	// DefaultVersion is seeded into the litespa_settings table when no
	// version row exists yet. The caller should resolve environment-specific
	// values (e.g. "v1.0.0" for prod, "b530c16" for dev) before passing.
	DefaultVersion string

	// CSP overrides the default Content-Security-Policy source allow-lists.
	// When zero-valued, sensible defaults matching the original doublefin SPA
	// are used.
	CSP CSPConfig

	// EmbeddedContent, when non-empty, is served directly instead of
	// fetching from the CDN. The Manager uses a static provider so the
	// database is never touched. Used for local development.
	EmbeddedContent string
}

// CSPConfig parameterises the Content-Security-Policy source allow-lists.
// Each field corresponds to a CSP directive. When a field is nil or empty,
// the module falls back to built-in defaults suitable for the doublefin SPA.
type CSPConfig struct {
	FontSrcs     []string
	ScriptSrcs   []string
	ConnectSrcs  []string
	StyleSrcs    []string
	ManifestSrcs []string
}
