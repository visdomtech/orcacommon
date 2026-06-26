package litespaserver

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"

	"golang.org/x/sync/singleflight"
)

// staticCacheCapacity bounds the in-memory static-file cache.
const staticCacheCapacity = 16

// staticRetriever serves an allow-list of static files from the CDN, caching
// successful responses in a bounded in-memory map.
type staticRetriever struct {
	client    *http.Client
	allowed   map[string]struct{}
	mu        sync.Mutex
	fileCache map[string]string
	sf        singleflight.Group
}

// newStaticRetriever builds a retriever whose allow-list is the supplied paths.
func newStaticRetriever(client *http.Client, paths []string) *staticRetriever {
	if client == nil {
		client = http.DefaultClient
	}
	allowed := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		if p != "" {
			allowed[p] = struct{}{}
		}
	}
	slog.Info("litespaserver static files", "paths", keysOf(allowed))
	return &staticRetriever{
		client:    client,
		allowed:   allowed,
		fileCache: make(map[string]string),
	}
}

// isStatic reports whether path is in the static-file allow-list.
func (s *staticRetriever) isStatic(path string) bool {
	_, ok := s.allowed[path]
	return ok
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

func keysOf(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
