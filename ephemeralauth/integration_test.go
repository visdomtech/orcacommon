package ephemeralauth

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/mux"
)

// TestIntegration exercises the full flow on a real gorilla/mux router:
// guest-session middleware on the page route, issuance endpoint, and a
// protected API route behind the verification middleware.
func TestIntegration(t *testing.T) {
	cfg := Config{
		SigningKey:      "integration-test-key-32bytes!!!!",
		TokenTTLSeconds: 120,
		Scopes:          []string{"public:read"},
	}
	issuer := cfg.Issuer()
	signingKey := []byte(cfg.SigningKey)

	// Build a real mux.Router.
	router := mux.NewRouter()

	// Page route — wrapped with GuestSession middleware.
	router.Handle("/", GuestSession(signingKey)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><body>Hello</body></html>"))
	})))

	// Issuance endpoint — stub-passing BotVerifier.
	router.Handle("/api/auth/ephemeral-token",
		IssuanceHandler(cfg, &stubVerifier{pass: true}, issuer))

	// Protected API route.
	router.Handle("/api/public/data",
		Protect(issuer, false, "public:read")(
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"data":"secret"}`))
			}),
		))

	// Scenario 1: No token → 401
	t.Run("NoToken_401", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/public/data", nil)
		req.RemoteAddr = "10.0.0.1:1234"
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", rec.Code)
		}
	})

	// Scenario 2: Full flow → 200
	t.Run("FullFlow_200", func(t *testing.T) {
		// Step 1: GET page → capture guest cookie.
		pageReq := httptest.NewRequest("GET", "/", nil)
		pageReq.RemoteAddr = "10.0.0.1:1234"
		pageReq.Header.Set("User-Agent", "IntegrationTest/1.0")
		pageRec := httptest.NewRecorder()
		router.ServeHTTP(pageRec, pageReq)

		if pageRec.Code != http.StatusOK {
			t.Fatalf("page status = %d, want 200", pageRec.Code)
		}

		var guestCookie *http.Cookie
		for _, c := range pageRec.Result().Cookies() {
			if c.Name == guestCookieName {
				guestCookie = c
				break
			}
		}
		if guestCookie == nil {
			t.Fatal("no guest cookie from page request")
		}

		// Step 2: POST issuance with cookie + bot token.
		issueBody := bytes.NewBufferString(`{"bot_token":"valid-bot-token"}`)
		issueReq := httptest.NewRequest("POST", "/api/auth/ephemeral-token", issueBody)
		issueReq.AddCookie(guestCookie)
		issueReq.RemoteAddr = "10.0.0.1:1234"
		issueReq.Header.Set("User-Agent", "IntegrationTest/1.0")
		issueRec := httptest.NewRecorder()
		router.ServeHTTP(issueRec, issueReq)

		if issueRec.Code != http.StatusOK {
			t.Fatalf("issuance status = %d, want 200", issueRec.Code)
		}

		var issueResp issuanceResponse
		if err := json.NewDecoder(issueRec.Body).Decode(&issueResp); err != nil {
			t.Fatalf("decode issuance response: %v", err)
		}
		if issueResp.Token == "" {
			t.Fatal("issued token is empty")
		}
		if issueResp.ExpiresIn > 180 {
			t.Errorf("expires_in = %d, want <= 180", issueResp.ExpiresIn)
		}

		// Step 3: GET protected route with Bearer + guest cookie.
		protReq := httptest.NewRequest("GET", "/api/public/data", nil)
		protReq.Header.Set("Authorization", "Bearer "+issueResp.Token)
		protReq.AddCookie(guestCookie)
		protReq.RemoteAddr = "10.0.0.1:1234"
		protReq.Header.Set("User-Agent", "IntegrationTest/1.0")
		protRec := httptest.NewRecorder()
		router.ServeHTTP(protRec, protReq)

		if protRec.Code != http.StatusOK {
			t.Errorf("protected status = %d, want 200", protRec.Code)
			t.Logf("body: %s", protRec.Body.String())
		}
	})

	// Scenario 3: Expired token → 401
	t.Run("ExpiredToken_401", func(t *testing.T) {
		// Create a valid guest cookie.
		cookie, sessionID := makeSessionCookie(t, signingKey)

		// Create an expired token bound to this session, IP, and UA.
		ipHash := HashContext(signingKey, "10.0.0.1")
		uaHash := HashContext(signingKey, "IntegrationTest/1.0")

		claims := &Claims{
			RegisteredClaims: jwt.RegisteredClaims{
				Subject:   sessionID,
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(-30 * time.Second)),
				IssuedAt:  jwt.NewNumericDate(time.Now().Add(-210 * time.Second)),
				Issuer:    "ephemeralauth",
			},
			IPHash: ipHash,
			UAHash: uaHash,
			Scopes: []string{"public:read"},
		}
		tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		expiredToken, err := tok.SignedString(signingKey)
		if err != nil {
			t.Fatalf("SignedString: %v", err)
		}

		req := httptest.NewRequest("GET", "/api/public/data", nil)
		req.Header.Set("Authorization", "Bearer "+expiredToken)
		req.AddCookie(cookie)
		req.RemoteAddr = "10.0.0.1:1234"
		req.Header.Set("User-Agent", "IntegrationTest/1.0")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", rec.Code)
		}
	})

	// Scenario 4: Token bound to different session/IP/UA → 403
	t.Run("DifferentBinding_403", func(t *testing.T) {
		// Create a valid guest cookie for the request.
		cookie, sessionID := makeSessionCookie(t, signingKey)

		// Create a token bound to THIS session but a DIFFERENT IP.
		ipHash := HashContext(signingKey, "99.99.99.99") // different IP
		uaHash := HashContext(signingKey, "IntegrationTest/1.0")

		iss := NewIssuer(signingKey, 120*time.Second)
		token, _, err := iss.Issue(sessionID, ipHash, uaHash, []string{"public:read"})
		if err != nil {
			t.Fatalf("Issue: %v", err)
		}

		req := httptest.NewRequest("GET", "/api/public/data", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.AddCookie(cookie)
		req.RemoteAddr = "10.0.0.1:1234" // different from token's 99.99.99.99
		req.Header.Set("User-Agent", "IntegrationTest/1.0")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Errorf("status = %d, want 403", rec.Code)
		}
	})
}
