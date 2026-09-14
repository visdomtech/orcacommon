package litespaserver

import (
	"crypto/hmac"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/visdomtech/orcacommon/ephemeralauth"
)

//go:embed testdata/embed
var testEmbeddedFS embed.FS

// newTestServer builds a Server backed by a static (no-DB) version provider
// pointed at the given CDN base URL.
func newTestServer(cdn, version string, client *http.Client) *Server {
	return &Server{
		cdn:        cdn,
		csp:        CSPConfig{},
		manager:    &Manager{cdn: cdn, provider: &staticProvider{v: version}},
		static:     newStaticRetriever(client, []string{"/unsubscribed.html"}),
		fetcher:    newFetcher(client),
		indexCache: make(map[string]string),
	}
}

func TestServeRoot_JSONRequestIs404(t *testing.T) {
	s := newTestServer("https://cdn.example", "v1.0.0", nil)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()

	s.ServeRoot(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestServeRoot_IndexHTMLInjectsNonce(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// index.html references the CDN (required) and carries a nonce="NONCE" attribute.
		_, _ = w.Write([]byte(`<script nonce="NONCE" src="` + srv.URL + `/app.js"></script>`))
	}))
	defer srv.Close()

	s := newTestServer(srv.URL, "v1.0.0", srv.Client())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()

	s.ServeRoot(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, `nonce="NONCE"`) {
		t.Errorf("literal nonce=\"NONCE\" was not replaced: %q", body)
	}
	if !strings.Contains(body, `nonce="`) {
		t.Errorf("expected nonce attribute in body: %q", body)
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "nonce-") {
		t.Errorf("CSP header missing nonce: %q", csp)
	}
	if got := rec.Header().Get("X-app-version"); got != "v1.0.0" {
		t.Errorf("X-app-version = %q, want v1.0.0", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
}

func TestServeRoot_InjectNonce_OnlyReplacesPlaceholder(t *testing.T) {
	// A second occurrence of "NONCE" in a comment or URL must not be replaced.
	body := `<script nonce="NONCE" src="/app.js"></script><!-- NONCE comment -->`
	nonce := "abc123"
	got := injectNonce(body, nonce)

	if !strings.Contains(got, `nonce="abc123"`) {
		t.Errorf("placeholder not replaced: %q", got)
	}
	if !strings.Contains(got, "<!-- NONCE comment -->") {
		t.Errorf("second NONCE occurrence was incorrectly replaced: %q", got)
	}
}

func TestServeRoot_IndexHTMLCachedPerVersion(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html>` + srv.URL + ` <script nonce="NONCE"></script></html>`))
	}))
	defer srv.Close()

	s := newTestServer(srv.URL, "v2.0.0", srv.Client())

	doReq := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header.Set("Accept", "text/html")
		rec := httptest.NewRecorder()
		s.ServeRoot(rec, req)
		return rec
	}

	doReq() // populate cache
	rec := doReq()

	if got := rec.Header().Get("X-fe-version-cache"); got != "v2.0.0" {
		t.Errorf("X-fe-version-cache = %q, want v2.0.0 (second request should hit cache)", got)
	}
}

func TestServeRoot_StaticFileProxied(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1.0.0/unsubscribed.html" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		_, _ = w.Write([]byte("unsub-page"))
	}))
	defer srv.Close()

	s := newTestServer(srv.URL, "v1.0.0", srv.Client())

	req := httptest.NewRequest(http.MethodGet, "/unsubscribed.html", nil)
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()

	s.ServeRoot(rec, req)

	if rec.Body.String() != "unsub-page" {
		t.Errorf("body = %q, want unsub-page", rec.Body.String())
	}
	// Static responses carry no nonce.
	if strings.Contains(rec.Header().Get("Content-Security-Policy"), "nonce-") {
		t.Error("static response should not carry a nonce CSP")
	}
}

func TestServeRoot_StaticFileCDNError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	s := newTestServer(srv.URL, "v1.0.0", srv.Client())

	req := httptest.NewRequest(http.MethodGet, "/unsubscribed.html", nil)
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()

	s.ServeRoot(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502 on CDN error", rec.Code)
	}
}

func TestServeRoot_FetchFailureFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusBadGateway)
	}))
	defer srv.Close()

	s := newTestServer(srv.URL, "v1.0.0", srv.Client())

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()

	s.ServeRoot(rec, req)

	if rec.Body.String() != fallbackBody+"\n" && rec.Body.String() != fallbackBody {
		t.Errorf("body = %q, want fallback", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}
}

func TestServeRoot_IndexHTMLSingleFlight(t *testing.T) {
	var fetchCount atomic.Int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetchCount.Add(1)
		_, _ = w.Write([]byte(`<script nonce="NONCE" src="` + srv.URL + `/app.js"></script>`))
	}))
	defer srv.Close()

	s := newTestServer(srv.URL, "v3.0.0", srv.Client())

	const n = 20
	done := make(chan struct{}, n)
	for i := 0; i < n; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("Accept", "text/html")
			rec := httptest.NewRecorder()
			s.ServeRoot(rec, req)
		}()
	}
	for i := 0; i < n; i++ {
		<-done
	}

	// All 20 concurrent cache misses should collapse to at most a few CDN calls.
	if got := fetchCount.Load(); got > 3 {
		t.Errorf("CDN fetch count = %d, singleflight should collapse concurrent misses", got)
	}
}

// newEmbeddedTestServer builds a Server backed by an embed.FS and a static
// version provider. The FS is expected to contain files under "testdata/embed".
func newEmbeddedTestServer(f embed.FS) *Server {
	sub, err := fs.Sub(f, "testdata/embed")
	if err != nil {
		panic(err)
	}
	return &Server{
		cdn:        "https://cdn.example",
		embedded:   sub,
		csp:        CSPConfig{},
		manager:    &Manager{cdn: "https://cdn.example", provider: &staticProvider{v: "embedded"}},
		static:     newStaticRetriever(nil, []string{"/unsubscribed.html"}),
		fetcher:    newFetcher(nil),
		indexCache: make(map[string]string),
	}
}

// newEmbeddedTestServerWithPatterns builds a Server backed by an embed.FS and
// glob-based static paths (e.g. "/assets/*").
func newEmbeddedTestServerWithPatterns(f embed.FS, patterns []string) *Server {
	sub, err := fs.Sub(f, "testdata/embed")
	if err != nil {
		panic(err)
	}
	return &Server{
		cdn:        "https://cdn.example",
		embedded:   sub,
		csp:        CSPConfig{},
		manager:    &Manager{cdn: "https://cdn.example", provider: &staticProvider{v: "embedded"}},
		static:     newStaticRetriever(nil, patterns),
		fetcher:    newFetcher(nil),
		indexCache: make(map[string]string),
	}
}

func TestServeRoot_EmbeddedFS_IndexHTML(t *testing.T) {
	s := newEmbeddedTestServer(testEmbeddedFS)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()

	s.ServeRoot(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, `nonce="NONCE"`) {
		t.Errorf("literal nonce=\"NONCE\" was not replaced: %q", body)
	}
	if !strings.Contains(body, `nonce="`) {
		t.Errorf("expected nonce attribute in body: %q", body)
	}
	if !strings.Contains(body, "<title>Test SPA</title>") {
		t.Errorf("expected embedded index.html content: %q", body)
	}
	if got := rec.Header().Get("X-app-version"); got != "embedded" {
		t.Errorf("X-app-version = %q, want embedded", got)
	}
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "nonce-") {
		t.Errorf("CSP header missing nonce: %q", csp)
	}
}

func TestServeRoot_EmbeddedFS_StaticFileGlobPattern(t *testing.T) {
	s := newEmbeddedTestServerWithPatterns(testEmbeddedFS, []string{"/assets/*"})

	req := httptest.NewRequest(http.MethodGet, "/assets/app.js", nil)
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()

	s.ServeRoot(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "test app") {
		t.Errorf("expected embedded asset content: %q", body)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "javascript") {
		t.Errorf("Content-Type = %q, want javascript", ct)
	}
	// Static responses carry no nonce.
	if strings.Contains(rec.Header().Get("Content-Security-Policy"), "nonce-") {
		t.Error("static response should not carry a nonce CSP")
	}
}

func TestServeRoot_EmbeddedFS_StaticFile(t *testing.T) {
	s := newEmbeddedTestServer(testEmbeddedFS)

	req := httptest.NewRequest(http.MethodGet, "/unsubscribed.html", nil)
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()

	s.ServeRoot(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "unsubscribed") {
		t.Errorf("expected embedded static file content: %q", body)
	}
	// Static responses carry no nonce.
	if strings.Contains(rec.Header().Get("Content-Security-Policy"), "nonce-") {
		t.Error("static response should not carry a nonce CSP")
	}
}

func TestResolveEmbedded_NilFS(t *testing.T) {
	got := resolveEmbedded(nil)
	if got != nil {
		t.Errorf("resolveEmbedded(nil) = %v, want nil", got)
	}
}

func TestResolveEmbedded_ValidFS(t *testing.T) {
	sub, err := fs.Sub(testEmbeddedFS, "testdata/embed")
	if err != nil {
		t.Fatal(err)
	}
	got := resolveEmbedded(sub)
	if got == nil {
		t.Error("resolveEmbedded(valid FS) = nil, want non-nil")
	}
}

func TestResolveEmbedded_MissingIndexHTML(t *testing.T) {
	// An FS with files but no index.html — should return nil (and log a warning).
	badFS := fstest.MapFS{
		"other.html": &fstest.MapFile{Data: []byte("not index")},
	}
	got := resolveEmbedded(badFS)
	if got != nil {
		t.Errorf("resolveEmbedded(FS without index.html) = %v, want nil", got)
	}
}

func TestServeRoot_EmbeddedFS_StaticFallbackToCDN(t *testing.T) {
	// embed.FS has index.html but NOT a CDN-only static file.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("from-cdn"))
	}))
	defer srv.Close()

	sub, err := fs.Sub(testEmbeddedFS, "testdata/embed")
	if err != nil {
		t.Fatal(err)
	}

	s := &Server{
		cdn:        srv.URL,
		embedded:   sub,
		csp:        CSPConfig{},
		manager:    &Manager{cdn: srv.URL, provider: &staticProvider{v: "embedded"}},
		static:     newStaticRetriever(srv.Client(), []string{"/cdn-only.html"}),
		fetcher:    newFetcher(srv.Client()),
		indexCache: make(map[string]string),
	}

	req := httptest.NewRequest(http.MethodGet, "/cdn-only.html", nil)
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()

	s.ServeRoot(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != "from-cdn" {
		t.Errorf("body = %q, want from-cdn (CDN fallback)", got)
	}
}

func TestServeRoot_PublicAuth_GuestCookieSet(t *testing.T) {
	sub, err := fs.Sub(testEmbeddedFS, "testdata/embed")
	if err != nil {
		t.Fatal(err)
	}

	gm := ephemeralauth.GuestSession([]byte("test-signing-key-for-litespa-test"))
	s := &Server{
		cdn:      "https://cdn.example",
		embedded: sub,
		csp:      CSPConfig{},
		manager:  &Manager{cdn: "https://cdn.example", provider: &staticProvider{v: "embedded"}},
		static:   newStaticRetriever(nil, nil),
		fetcher:  newFetcher(nil),
		indexCache: make(map[string]string),
		// Wire guest middleware and its cached wrapper.
		guestMiddleware: gm,
		publicAuthCfg: &ephemeralauth.Config{
			SigningKey: "test-signing-key-for-litespa-test",
		},
	}
	s.wrappedServeRoot = gm(http.HandlerFunc(s.serveRootInner))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()
	s.ServeRoot(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	// Should have a Set-Cookie for the guest session.
	var found bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == "guest_session" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected guest_session cookie to be set")
	}
}

func TestServeRoot_NoPublicAuth_NoGuestCookie(t *testing.T) {
	sub, err := fs.Sub(testEmbeddedFS, "testdata/embed")
	if err != nil {
		t.Fatal(err)
	}

	s := &Server{
		cdn:        "https://cdn.example",
		embedded:   sub,
		csp:        CSPConfig{},
		manager:    &Manager{cdn: "https://cdn.example", provider: &staticProvider{v: "embedded"}},
		static:     newStaticRetriever(nil, nil),
		fetcher:    newFetcher(nil),
		indexCache: make(map[string]string),
		// No PublicAuth configured.
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept", "text/html")
	rec := httptest.NewRecorder()
	s.ServeRoot(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	// Should NOT have a guest session cookie.
	for _, c := range rec.Result().Cookies() {
		if c.Name == "guest_session" {
			t.Error("guest_session cookie should NOT be set when PublicAuth is nil")
		}
	}
}

func TestPublicAuthHandler_NilWhenNotConfigured(t *testing.T) {
	s := &Server{
		indexCache: make(map[string]string),
	}
	if s.PublicAuthHandler() != nil {
		t.Error("PublicAuthHandler() should return nil when not configured")
	}
	if s.PublicAuthMiddleware() != nil {
		t.Error("PublicAuthMiddleware() should return nil when not configured")
	}
}

func TestPublicAuthHandler_NonNilWhenConfigured(t *testing.T) {
	cfg := &ephemeralauth.Config{
		SigningKey:      "test-key-for-auth-handler-test!!",
		TokenTTLSeconds: 120,
		TurnstileSecret: "test-turnstile-secret",
	}
	issuer := cfg.Issuer()
	s := &Server{
		indexCache:      make(map[string]string),
		publicAuthCfg:   cfg,
		issuer:          issuer,
		issuanceHandler: ephemeralauth.IssuanceHandler(*cfg, ephemeralauth.NewTurnstileVerifier(cfg.TurnstileSecret), issuer),
	}

	if s.PublicAuthHandler() == nil {
		t.Error("PublicAuthHandler() should return non-nil when configured")
	}
	if s.PublicAuthMiddleware() == nil {
		t.Error("PublicAuthMiddleware() should return non-nil when configured")
	}
}

func TestConfig_LogValue_RedactsSecrets(t *testing.T) {
	cfg := ephemeralauth.Config{
		SigningKey:      "super-secret-key",
		TurnstileSecret: "turnstile-secret-value",
	}
	val := cfg.LogValue()
	str := val.String()
	if strings.Contains(str, "super-secret-key") {
		t.Error("signing key leaked in LogValue output")
	}
	if strings.Contains(str, "turnstile-secret-value") {
		t.Error("turnstile secret leaked in LogValue output")
	}
	if !strings.Contains(str, "[REDACTED]") {
		t.Error("expected [REDACTED] in LogValue output")
	}
}

func TestPublicAuthMiddleware_RequiredScopes(t *testing.T) {
	cfg := &ephemeralauth.Config{
		SigningKey:     "test-key-for-scope-enforcement!!",
		TokenTTLSeconds: 120,
		Scopes:         []string{"public:read"},
		RequiredScopes: []string{"public:read"},
	}
	issuer := cfg.Issuer()
	s := &Server{
		indexCache:    make(map[string]string),
		publicAuthCfg: cfg,
		issuer:        issuer,
	}

	mw := s.PublicAuthMiddleware()
	if mw == nil {
		t.Fatal("PublicAuthMiddleware() returned nil")
	}

	// Create a token WITHOUT the required scope.
	signingKey := []byte(cfg.SigningKey)
	ipHash := ephemeralauth.HashContext(signingKey, "1.2.3.4")
	uaHash := ephemeralauth.HashContext(signingKey, "TestAgent")
	token, _, err := issuer.Issue("session-1", ipHash, uaHash, []string{"other:scope"})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}

	// Build request with token + guest cookie.
	cookie, _ := makeSessionCookieForTest(t, signingKey)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest("GET", "/api/data", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.AddCookie(cookie)
	req.RemoteAddr = "1.2.3.4:1234"
	req.Header.Set("User-Agent", "TestAgent")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 (missing required scope)", rec.Code)
	}
}

// makeSessionCookieForTest creates a valid guest cookie for litespaserver tests.
func makeSessionCookieForTest(t *testing.T, key []byte) (*http.Cookie, string) {
	t.Helper()
	id := make([]byte, 16)
	for i := range id {
		id[i] = byte(i)
	}
	h := hmacSHA256(id, key)
	value := encodeB64URL(id) + "." + encodeB64URL(h)
	return &http.Cookie{
		Name:  "guest_session",
		Value: value,
	}, encodeB64URL(id)
}

func hmacSHA256(data, key []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

func encodeB64URL(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}
