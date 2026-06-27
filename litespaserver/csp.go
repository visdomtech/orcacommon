package litespaserver

import (
	"crypto/rand"
	"strings"
)

// Default CSP source constants — used as fallbacks when CSPConfig fields are
// empty. These match the original doublefin SPA deployment.
const (
	cspSelf              = "'self'"
	cspDoublefinCDN      = "https://hc-cdn.doublefin.com"
	cspDoublefinCDNDaily = "https://hcdev-cdn.doublefin.com"
	cspGoogleAccounts    = "https://accounts.google.com"
	cspGoogleAPIs        = "https://apis.google.com"
	cspGoogleStorage     = "https://storage.googleapis.com"
	cspDoublefinAll      = "https://*.doublefin.com"
)

var (
	ScriptSrcAll   = []string{"*", "'unsafe-inline'", "'unsafe-eval'", "data:", "blob:"}
	StyleSrcAll    = []string{"*", "'unsafe-inline'", "data:", "blob:"}
	ConnectSrcAll  = []string{"*", "data:", "blob:"}
	FontSrcAll     = []string{"*", "data:", "blob:"}
	ManifestSrcAll = []string{"*", "data:", "blob:"}
)

// defaultFontSrcs is the fallback font-src allow-list.
var defaultFontSrcs = []string{cspSelf, cspDoublefinCDN, cspDoublefinCDNDaily}

// defaultManifestSrcs is the fallback manifest-src allow-list.
var defaultManifestSrcs = []string{cspSelf, cspDoublefinCDN, cspDoublefinCDNDaily}

// defaultScriptSrcs is the fallback script-src allow-list.
var defaultScriptSrcs = []string{cspSelf, cspDoublefinCDN, cspDoublefinCDNDaily, cspGoogleAccounts, cspGoogleAPIs}

// defaultConnectSrcs is the fallback connect-src allow-list.
var defaultConnectSrcs = []string{
	cspSelf, cspDoublefinCDN, cspDoublefinCDNDaily, cspGoogleAccounts, cspGoogleAPIs,
	cspGoogleStorage, cspDoublefinAll,
}

// defaultStyleSrcs is the fallback style-src allow-list (excluding the nonce,
// which is injected per-request).
var defaultStyleSrcs = []string{
	"'unsafe-hashes'",
	cspSelf, cspDoublefinCDN, cspDoublefinCDNDaily, cspGoogleAccounts,
	"'sha256-47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU='",
	"'sha256-ZdHxw9eWtnxUb3mk6tBS+gIiVUPE3pGM470keHPDFlE='",
	"'sha256-dCNOmK/nSY+12vHzasLXiswzlGT5UHA7jAGYkvmCuQs='",
	"'sha256-vYd+FsML43MBXhP+pXOhW9h0Cdq43hkCe4Im/yyvhss='",
	"'sha256-aqNNdDLnnrDOnTNdkJpYlAxKVJtLt9CtFLklmInuUAE='",
	"'sha256-ez18eoOA3hwtNh/P9f8KPxcOdorR7TwpKOdo2DZqgV4='",
	"'sha256-HLH8Aj1d/u9Yre92z4+ZKgw39ExiiScZBG3W1Vf5o9g='",
	"'sha256-deHIoPlRijnpfbTDYsK+8JmDfUBmpwpnb0L/SUV8NeU='",
	"'sha256-NcGgkTn5U5sLsc2+SWulyTfIqyyjjm9JD9cw9aYa0ek='",
	"'sha256-CemOtDQpZ5U2c1aMwNP33cQ4r487RzoaoDL7XkXAlKQ='",
	"'sha256-uCvsNvXCD9uUw1ELbn+9KbUqNulkAQeIqVkQL44hPVE='",
	"'sha256-ByOXYIXIkfNC3flUR/HoxR4Ak0pjOEF1q8XmtuIa6po='",
}

// cspRule builds the Content-Security-Policy header value using the provided
// CSPConfig sources (falling back to defaults) and an optional per-request nonce.
// When nonce is non-empty it is appended to style-src as 'nonce-<nonce>'.
func cspRule(csp CSPConfig, nonce string) string {
	fontSrcs := orDefault(csp.FontSrcs, defaultFontSrcs)
	manifestSrcs := orDefault(csp.ManifestSrcs, defaultManifestSrcs)
	scriptSrcs := orDefault(csp.ScriptSrcs, defaultScriptSrcs)
	connectSrcs := orDefault(csp.ConnectSrcs, defaultConnectSrcs)
	styleSrcs := orDefault(csp.StyleSrcs, defaultStyleSrcs)

	styleSrc := strings.Join(styleSrcs, " ")
	if nonce != "" {
		styleSrc += " 'nonce-" + nonce + "'"
	}

	directives := []string{
		"default-src 'self'",
		"object-src blob:",
		"font-src " + strings.Join(fontSrcs, " "),
		"manifest-src " + strings.Join(manifestSrcs, " "),
		"script-src " + strings.Join(scriptSrcs, " "),
		"media-src 'none'",
		"connect-src " + strings.Join(connectSrcs, " "),
		"frame-src *",
		"img-src * blob: data:",
		"style-src " + styleSrc,
	}
	return strings.Join(directives, "; ")
}

// orDefault returns v when non-empty, otherwise def.
func orDefault(v, def []string) []string {
	if len(v) > 0 {
		return v
	}
	return def
}

const nonceAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// generateNonce returns a random alphanumeric string of length n. It uses
// crypto/rand and returns an error if entropy is unavailable — callers must
// not serve a page with a predictable nonce, as that defeats CSP isolation.
func generateNonce(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i, b := range buf {
		buf[i] = nonceAlphabet[int(b)%len(nonceAlphabet)]
	}
	return string(buf), nil
}
