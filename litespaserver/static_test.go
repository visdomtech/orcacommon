package litespaserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestStaticRetriever_IsStatic(t *testing.T) {
	s := newStaticRetriever(nil, []string{"/unsubscribed.html", "/custom.html"})

	for _, p := range []string{"/unsubscribed.html", "/custom.html"} {
		if !s.isStatic(p) {
			t.Errorf("isStatic(%q) = false, want true", p)
		}
	}
	if s.isStatic("/not-allowed.html") {
		t.Error("isStatic(/not-allowed.html) = true, want false")
	}
}

func TestStaticRetriever_IsStatic_GlobPattern(t *testing.T) {
	s := newStaticRetriever(nil, []string{"/assets/*", "/unsubscribed.html"})

	// Exact match still works.
	if !s.isStatic("/unsubscribed.html") {
		t.Error("isStatic(/unsubscribed.html) = false, want true")
	}
	// Single-segment glob matches.
	for _, p := range []string{"/assets/index.js", "/assets/style.css", "/assets/vendor-abc123.js"} {
		if !s.isStatic(p) {
			t.Errorf("isStatic(%q) = false, want true (should match /assets/*)", p)
		}
	}
	// Non-matching paths.
	for _, p := range []string{"/other/file.js", "/assetsx/foo.js", "/"} {
		if s.isStatic(p) {
			t.Errorf("isStatic(%q) = true, want false", p)
		}
	}
	// Single-segment * does not match nested paths.
	if s.isStatic("/assets/sub/deep.js") {
		t.Error("isStatic(/assets/sub/deep.js) = true, want false (* does not cross /)")
	}
}

func TestStaticRetriever_IsStatic_DoublestarPattern(t *testing.T) {
	s := newStaticRetriever(nil, []string{"/assets/**"})

	// Matches single-segment paths.
	for _, p := range []string{"/assets/index.js", "/assets/style.css"} {
		if !s.isStatic(p) {
			t.Errorf("isStatic(%q) = false, want true (should match /assets/**)", p)
		}
	}
	// Matches nested paths (** crosses /).
	for _, p := range []string{"/assets/sub/deep.js", "/assets/a/b/c.js"} {
		if !s.isStatic(p) {
			t.Errorf("isStatic(%q) = false, want true (** should match nested paths)", p)
		}
	}
	// Non-matching paths.
	for _, p := range []string{"/other/file.js", "/assetsx/foo.js", "/"} {
		if s.isStatic(p) {
			t.Errorf("isStatic(%q) = true, want false", p)
		}
	}
}

func TestStaticRetriever_Retrieve_Caches(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		_, _ = w.Write([]byte("static-body"))
	}))
	defer srv.Close()

	s := newStaticRetriever(srv.Client(), []string{"/unsubscribed.html"})
	ctx := context.Background()

	for range 3 {
		body, err := s.retrieve(ctx, srv.URL, "v1.0.0", "/unsubscribed.html")
		if err != nil {
			t.Fatalf("retrieve: %v", err)
		}
		if body != "static-body" {
			t.Errorf("body = %q", body)
		}
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("CDN hits = %d, want 1 (subsequent calls should be cached)", got)
	}
}

func TestStaticRetriever_Retrieve_ErrorNotCached(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "missing", http.StatusNotFound)
	}))
	defer srv.Close()

	s := newStaticRetriever(srv.Client(), []string{"/unsubscribed.html"})
	if _, err := s.retrieve(context.Background(), srv.URL, "v1.0.0", "/unsubscribed.html"); err == nil {
		t.Fatal("expected error on non-2xx response")
	}
}
