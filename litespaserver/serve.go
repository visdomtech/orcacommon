package litespaserver

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"golang.org/x/sync/singleflight"

	"github.com/jackc/pgx/v5/pgxpool"
)

// nonceLength is the per-request CSP nonce length.
const nonceLength = 12

// indexCacheCapacity bounds the per-version index.html cache.
const indexCacheCapacity = 10

// fallbackBody is returned when the SPA index.html cannot be fetched.
const fallbackBody = "Something unexpected happened - We'll be right back."

// Server serves the CDN-hosted SPA: index.html (with per-request CSP nonce) and
// an allow-list of static files. When embedded content is configured, it is
// served directly without CDN fetches.
type Server struct {
	cdn      string
	embedded string // non-empty: baked-in HTML served instead of CDN fetch
	csp      CSPConfig
	manager  *Manager
	static   *staticRetriever
	fetcher  *fetcher

	mu         sync.Mutex
	indexCache map[string]string
	sf         singleflight.Group
}

// NewServer builds a Server from the provided Config. pool is used for the
// version manager's DB-backed provider (ignored when cfg.CDNVersion or
// cfg.EmbeddedContent is set).
func NewServer(ctx context.Context, pool *pgxpool.Pool, cfg Config) *Server {
	return &Server{
		cdn:        cfg.CDNPrefix,
		embedded:   cfg.EmbeddedContent,
		csp:        cfg.CSP,
		manager:    NewManager(ctx, pool, cfg.CDNPrefix, cfg.CDNVersion, cfg.DefaultVersion, cfg.EmbeddedContent != ""),
		static:     newStaticRetriever(nil, cfg.StaticPaths),
		fetcher:    newFetcher(nil),
		indexCache: make(map[string]string),
	}
}

// RefreshVersion reloads the live version from the database.
func (s *Server) RefreshVersion(ctx context.Context) {
	s.manager.ForceRefresh(ctx)
}

// Manager exposes the underlying version manager (e.g. for SetVersion wiring).
func (s *Server) Manager() *Manager { return s.manager }

// FlushCache invalidates the in-memory index.html cache. Called via Manager.OnChange
// so that a version update automatically evicts stale cached pages.
func (s *Server) FlushCache() {
	s.mu.Lock()
	clear(s.indexCache)
	s.mu.Unlock()
}

// ServeRoot handles a request for the SPA root or a static file. JSON requests
// get a 404, static files are proxied from the CDN, and everything else serves
// index.html with a fresh CSP nonce.
func (s *Server) ServeRoot(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// JSON callers do not want HTML; return 404.
	if strings.Contains(r.Header.Get("Accept"), "application/json") {
		http.NotFound(w, r)
		return
	}

	version := s.manager.Version(ctx)
	path := r.URL.Path

	// Static files: proxy from CDN with base security headers, no nonce.
	if s.static.isStatic(path) {
		body, err := s.static.retrieve(ctx, s.cdn, version, path)
		if err != nil {
			slog.WarnContext(ctx, "litespaserver: serve static file failed", "path", path, "err", err)
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
			return
		}
		s.setBaseHeaders(w, "")
		_, _ = w.Write([]byte(body))
		return
	}

	// index.html: per-request nonce, version header, version-keyed cache.
	nonce, err := generateNonce(nonceLength)
	if err != nil {
		slog.ErrorContext(ctx, "litespaserver: nonce generation failed", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	s.setBaseHeaders(w, nonce)
	w.Header().Set("X-app-version", version)

	// Embedded mode: serve baked-in HTML, no CDN fetch or cache needed.
	if s.embedded != "" {
		_, _ = w.Write([]byte(injectNonce(s.embedded, nonce)))
		return
	}

	if cached, ok := s.indexLookup(version); ok {
		w.Header().Set("X-fe-version-cache", version)
		_, _ = w.Write([]byte(injectNonce(cached, nonce)))
		return
	}

	// Collapse concurrent cache misses for the same version to a single CDN fetch.
	result, _, shared := s.sf.Do(version, func() (any, error) {
		fv, ok := s.fetcher.fetch(ctx, s.cdn, version)
		if !ok {
			return nil, errors.New("fetch failed")
		}
		s.indexStore(version, fv.content)
		return fv, nil
	})
	_ = shared

	if result != nil {
		fv := result.(fetchedVersion)
		w.Header().Set("X-fe-version-url", fv.url)
		_, _ = w.Write([]byte(injectNonce(fv.content, nonce)))
		return
	}

	slog.ErrorContext(ctx, "litespaserver: failed to serve index.html", "version", version)
	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte(fallbackBody))
}

// setBaseHeaders applies the no-store + security + CSP headers. A non-empty
// nonce is woven into the CSP style-src.
func (s *Server) setBaseHeaders(w http.ResponseWriter, nonce string) {
	h := w.Header()
	h.Set("Cache-Control", "no-store, max-age=0")
	h.Set("Content-Type", "text/html")
	h.Set("X-Frame-Options", "SAMEORIGIN")
	h.Set("Referrer-Policy", "origin-when-cross-origin")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", cspRule(s.csp, nonce))
}

func (s *Server) indexLookup(version string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.indexCache[version]
	return v, ok
}

func (s *Server) indexStore(version, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.indexCache) >= indexCacheCapacity {
		for k := range s.indexCache {
			delete(s.indexCache, k)
			break
		}
	}
	s.indexCache[version] = body
}

// injectNonce replaces exactly the nonce="NONCE" attribute the SPA build emits
// with the per-request nonce value.
func injectNonce(body, nonce string) string {
	return strings.Replace(body, `nonce="NONCE"`, `nonce="`+nonce+`"`, 1)
}
