package litespaserver

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path"
	"sync"

	"golang.org/x/sync/singleflight"
)

// staticCacheCapacity bounds the in-memory static-file cache.
const staticCacheCapacity = 16

// staticRetriever serves an allow-list of static files from the CDN, caching
// successful responses in a bounded in-memory map. Paths may be exact matches
// (e.g. "/unsubscribed.html") or glob patterns (e.g. "/assets/*").
type staticRetriever struct {
	client    *http.Client
	patterns  []string // exact paths or glob patterns (path.Match syntax)
	mu        sync.Mutex
	fileCache map[string]string
	sf        singleflight.Group
}

// newStaticRetriever builds a retriever whose allow-list is the supplied paths.
// Each path may be an exact match (e.g. "/unsubscribed.html") or a glob pattern
// using path.Match syntax (e.g. "/assets/*").
func newStaticRetriever(client *http.Client, paths []string) *staticRetriever {
	if client == nil {
		client = http.DefaultClient
	}
	var patterns []string
	for _, p := range paths {
		if p != "" {
			patterns = append(patterns, p)
		}
	}
	slog.Info("litespaserver static files", "patterns", patterns)
	return &staticRetriever{
		client:    client,
		patterns:  patterns,
		fileCache: make(map[string]string),
	}
}

// isStatic reports whether path matches any entry in the static-file allow-list.
// Entries may be exact paths or glob patterns (path.Match syntax).
// Malformed patterns (e.g. unterminated brackets) silently fail to match.
func (s *staticRetriever) isStatic(reqPath string) bool {
	for _, p := range s.patterns {
		if matched, _ := path.Match(p, reqPath); matched {
			return true
		}
	}
	return false
}

// retrieve fetches {cdn}/{version}{path} from the CDN, caching successful
// responses keyed by full URL. On a non-2xx response it returns the body and an
// error.
func (s *staticRetriever) retrieve(ctx context.Context, cdn, version, path string) (string, error) {
	url := fmt.Sprintf("%s/%s%s", cdn, version, path)

	s.mu.Lock()
	if cached, ok := s.fileCache[url]; ok {
		s.mu.Unlock()
		return cached, nil
	}
	s.mu.Unlock()

	// Collapse concurrent first-fetches of the same URL to a single CDN call.
	result, err, _ := s.sf.Do(url, func() (any, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return "", err
		}
		resp, err := s.client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()

		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", err
		}
		body := string(raw)

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			slog.WarnContext(ctx, "litespaserver: fetch static file failed", "url", url, "status", resp.Status)
			return "", fmt.Errorf("litespaserver: fetch %s: status %s", url, resp.Status)
		}

		s.put(url, body)
		return body, nil
	})
	if err != nil {
		return "", err
	}
	return result.(string), nil
}

// put stores a cache entry, evicting an arbitrary entry when at capacity.
func (s *staticRetriever) put(url, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.fileCache) >= staticCacheCapacity {
		for k := range s.fileCache {
			delete(s.fileCache, k)
			break
		}
	}
	s.fileCache[url] = body
}
