package ephemeralauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTurnstile_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
			t.Errorf("content-type = %s, want application/x-www-form-urlencoded", ct)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(turnstileResponse{Success: true})
	}))
	defer srv.Close()

	tv := &TurnstileVerifier{
		secret:     "test-secret",
		endpoint:   srv.URL,
		httpClient: srv.Client(),
	}

	ok, err := tv.Verify(context.Background(), "valid-token", "1.2.3.4")
	if err != nil {
		t.Fatalf("Verify() error: %v", err)
	}
	if !ok {
		t.Error("Verify() = false, want true for success response")
	}
}

func TestTurnstile_Failure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(turnstileResponse{Success: false})
	}))
	defer srv.Close()

	tv := &TurnstileVerifier{
		secret:     "test-secret",
		endpoint:   srv.URL,
		httpClient: srv.Client(),
	}

	ok, err := tv.Verify(context.Background(), "invalid-token", "1.2.3.4")
	if err != nil {
		t.Fatalf("Verify() error: %v", err)
	}
	if ok {
		t.Error("Verify() = true, want false for failure response")
	}
}

func TestTurnstile_HTTPError_FailClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer srv.Close()

	tv := &TurnstileVerifier{
		secret:     "test-secret",
		endpoint:   srv.URL,
		httpClient: srv.Client(),
	}

	ok, err := tv.Verify(context.Background(), "some-token", "1.2.3.4")
	if err == nil {
		t.Fatal("Verify() error = nil, want non-nil for HTTP 500")
	}
	if ok {
		t.Error("Verify() = true, want false (fail-closed)")
	}
}

func TestTurnstile_MalformedJSON_FailClosed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("{invalid json"))
	}))
	defer srv.Close()

	tv := &TurnstileVerifier{
		secret:     "test-secret",
		endpoint:   srv.URL,
		httpClient: srv.Client(),
	}

	ok, err := tv.Verify(context.Background(), "some-token", "1.2.3.4")
	if err == nil {
		t.Fatal("Verify() error = nil, want non-nil for malformed JSON")
	}
	if ok {
		t.Error("Verify() = true, want false (fail-closed)")
	}
}

func TestTurnstile_EmptyToken(t *testing.T) {
	tv := &TurnstileVerifier{
		secret:   "test-secret",
		endpoint: "http://unused",
	}

	ok, err := tv.Verify(context.Background(), "", "1.2.3.4")
	if err == nil {
		t.Fatal("Verify() error = nil for empty token")
	}
	if ok {
		t.Error("Verify() = true, want false for empty token")
	}
}
