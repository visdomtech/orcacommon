package litespaserver

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// fetcher retrieves the SPA index.html for a given version from the CDN.
type fetcher struct {
	client *http.Client
}

// newFetcher returns a fetcher using the provided HTTP client (or http.DefaultClient
// when nil).
func newFetcher(client *http.Client) *fetcher {
	if client == nil {
		client = http.DefaultClient
	}
	return &fetcher{client: client}
}

// fetchedVersion holds a successfully retrieved index.html and its source URL.
type fetchedVersion struct {
	url     string
	content string
}

// fetch retrieves {cdn}/{version}/index.html. It returns a fetchedVersion only
// when the response is 2xx AND the body contains the CDN prefix — the same
// validation the original fetcher performs to guard against the CDN serving an
// unexpected (e.g. error) page that still returns 200.
func (f *fetcher) fetch(ctx context.Context, cdn, version string) (fetchedVersion, bool) {
	url := fmt.Sprintf("%s/%s/index.html", cdn, version)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		slog.WarnContext(ctx, "litespaserver: build index.html request failed", "url", url, "err", err)
		return fetchedVersion{}, false
	}

	resp, err := f.client.Do(req)
	if err != nil {
		slog.WarnContext(ctx, "litespaserver: fetch index.html failed", "url", url, "err", err)
		return fetchedVersion{}, false
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		slog.WarnContext(ctx, "litespaserver: read index.html body failed", "url", url, "err", err)
		return fetchedVersion{}, false
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		slog.WarnContext(ctx, "litespaserver: fetch index.html non-2xx", "url", url, "status", resp.Status)
		return fetchedVersion{}, false
	}

	if !strings.Contains(string(body), cdn) {
		slog.WarnContext(ctx, "litespaserver: index.html missing cdn prefix, invalid response", "url", url)
		return fetchedVersion{}, false
	}

	return fetchedVersion{url: url, content: string(body)}, true
}
