package litespaserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetcher_Fetch_Success(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1.0.0/index.html" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		// Body must contain the CDN prefix to be considered valid.
		_, _ = w.Write([]byte("<html>asset base " + srv.URL + "/app.js</html>"))
	}))
	defer srv.Close()

	f := newFetcher(srv.Client())
	got, ok := f.fetch(context.Background(), srv.URL, "v1.0.0")
	if !ok {
		t.Fatal("expected fetch to succeed")
	}
	if got.url != srv.URL+"/v1.0.0/index.html" {
		t.Errorf("url = %q", got.url)
	}
}

func TestFetcher_Fetch_MissingCDNPrefix(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 200 but body does not reference the CDN → invalid.
		_, _ = w.Write([]byte("<html>error page</html>"))
	}))
	defer srv.Close()

	f := newFetcher(srv.Client())
	if _, ok := f.fetch(context.Background(), srv.URL, "v1.0.0"); ok {
		t.Fatal("expected fetch to fail when body lacks CDN prefix")
	}
}

func TestFetcher_Fetch_Non2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()

	f := newFetcher(srv.Client())
	if _, ok := f.fetch(context.Background(), srv.URL, "v9.9.9"); ok {
		t.Fatal("expected fetch to fail on non-2xx")
	}
}
