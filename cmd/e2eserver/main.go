// Command e2eserver starts an ephemeral e2e test server that wires both
// standalone ephemeralauth routes and litespaserver PublicAuth integration
// routes on a single gorilla/mux router.
//
// The server writes a manifest JSON file with its base URL and blocks
// until SIGTERM/SIGINT.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"syscall"
	"testing/fstest"

	"github.com/gorilla/mux"
	"github.com/visdomtech/orcacommon/ephemeralauth"
	"github.com/visdomtech/orcacommon/litespaserver"
)

const (
	manifestPath = "/tmp/orcacommon-e2e-server.json"
	signingKey   = "e2e-test-signing-key-must-be-long-enough"
)

func main() {
	// Ephemeral auth config for standalone routes.
	cfg := ephemeralauth.Config{
		SigningKey:        signingKey,
		TokenTTLSeconds:   60, // minimum for faster expired-token tests
		TurnstileSitekey:  ephemeralauth.TurnstileTestAlwaysPassSitekey,
		TurnstileSecret:   ephemeralauth.TurnstileTestAlwaysPassSecret,
		TrustProxy:        true,
		Scopes:            []string{"public:read"},
		RequiredScopes:    []string{"public:read"},
	}

	issuer := cfg.Issuer()

	// Turnstile verifiers for different test scenarios.
	passVerifier := ephemeralauth.NewTurnstileVerifier(ephemeralauth.TurnstileTestAlwaysPassSecret)
	failVerifier := ephemeralauth.NewTurnstileVerifier(ephemeralauth.TurnstileTestAlwaysFailSecret)
	expiredVerifier := ephemeralauth.NewTurnstileVerifier(ephemeralauth.TurnstileTestTokenExpiredSecret)

	// Admin-scoped config (issues tokens with admin:write scope).
	adminCfg := ephemeralauth.Config{
		SigningKey:      signingKey,
		TokenTTLSeconds: 60,
		TurnstileSecret: ephemeralauth.TurnstileTestAlwaysPassSecret,
		TrustProxy:      true,
		Scopes:          []string{"public:read", "admin:write"},
	}

	// LiteSPA server with PublicAuth.
	spaFS := fstest.MapFS{
		"index.html": &fstest.MapFile{
			Data: []byte(`<!DOCTYPE html><html><head><style nonce="NONCE"></style></head><body><h1>E2E Test SPA</h1></body></html>`),
		},
	}
	spaServer := litespaserver.NewServer(context.Background(), nil, litespaserver.Config{
		EmbeddedContent: spaFS,
		PublicAuth: &ephemeralauth.Config{
			SigningKey:      signingKey,
			TokenTTLSeconds: 60,
			TurnstileSitekey: ephemeralauth.TurnstileTestAlwaysPassSitekey,
			TurnstileSecret: ephemeralauth.TurnstileTestAlwaysPassSecret,
			TrustProxy:      true,
			Scopes:          []string{"public:read"},
			RequiredScopes:  []string{"public:read"},
		},
	})

	// Build the gorilla/mux router.
	r := mux.NewRouter()

	// --- Standalone ephemeralauth routes ---

	// Health check.
	r.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
	}).Methods("GET")

	// SPA page route with guest session middleware.
	r.Handle("/",
		ephemeralauth.GuestSession([]byte(signingKey))(
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, "<html><body>E2E Server</body></html>")
			}),
		),
	).Methods("GET")

	// Token issuance with always-pass Turnstile.
	r.Handle("/api/auth/ephemeral-token",
		ephemeralauth.IssuanceHandler(cfg, passVerifier, issuer),
	).Methods("POST")

	// Token issuance with always-fail Turnstile.
	r.Handle("/api/auth/ephemeral-token-fail",
		ephemeralauth.IssuanceHandler(cfg, failVerifier, issuer),
	).Methods("POST")

	// Token issuance with token-expired Turnstile.
	r.Handle("/api/auth/ephemeral-token-expired",
		ephemeralauth.IssuanceHandler(cfg, expiredVerifier, issuer),
	).Methods("POST")

	// Token issuance with admin scopes.
	adminIssuer := adminCfg.Issuer()
	adminPassVerifier := ephemeralauth.NewTurnstileVerifier(ephemeralauth.TurnstileTestAlwaysPassSecret)
	r.Handle("/api/auth/ephemeral-token-admin",
		ephemeralauth.IssuanceHandler(adminCfg, adminPassVerifier, adminIssuer),
	).Methods("POST")

	// Protected endpoint requiring "public:read" scope.
	r.Handle("/api/public/data",
		ephemeralauth.Protect(issuer, []byte(signingKey), true, "public:read")(
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"data":"public"}`)
			}),
		),
	).Methods("GET")

	// Protected endpoint requiring "admin:write" scope.
	r.Handle("/api/admin/data",
		ephemeralauth.Protect(issuer, []byte(signingKey), true, "admin:write")(
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"data":"admin"}`)
			}),
		),
	).Methods("GET")

	// --- LiteSPA server routes with PublicAuth ---

	r.HandleFunc("/spa/{path:.*}", spaServer.ServeRoot).Methods("GET")

	if handler := spaServer.PublicAuthHandler(); handler != nil {
		r.Handle("/api/spa/auth/ephemeral-token", handler).Methods("POST")
	}

	if mw := spaServer.PublicAuthMiddleware(); mw != nil {
		r.Handle("/api/spa/public/data",
			mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"data":"spa-public"}`)
			})),
		).Methods("GET")
	}

	// Start the test server.
	ts := httptest.NewServer(r)
	defer ts.Close()

	// Write the manifest (0600 — owner-only, prevents local symlink attacks).
	manifest := map[string]string{
		"base_url": ts.URL,
		"pid":      fmt.Sprintf("%d", os.Getpid()),
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		slog.Error("marshal manifest", "error", err)
		os.Exit(1)
	}
	if err := os.WriteFile(manifestPath, data, 0600); err != nil {
		slog.Error("write manifest", "error", err)
		os.Exit(1)
	}
	defer os.Remove(manifestPath) // clean up on panic as well as normal exit
	slog.Info("e2e server listening", "base_url", ts.URL, "manifest", manifestPath)

	// Block until signal.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	sig := <-sigCh
	slog.Info("received signal, shutting down", "signal", sig)

	// Clean up manifest.
	os.Remove(manifestPath)
}
