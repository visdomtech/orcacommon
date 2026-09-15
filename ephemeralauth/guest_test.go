package ephemeralauth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGuest_CookieSetOnFirstVisit(t *testing.T) {
	mw := GuestSession(testKey)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	cookies := rec.Result().Cookies()
	var found *http.Cookie
	for _, c := range cookies {
		if c.Name == guestCookieName {
			found = c
			break
		}
	}
	if found == nil {
		t.Fatal("guest session cookie not set")
	}

	// Verify cookie flags.
	if found.SameSite != http.SameSiteStrictMode {
		t.Errorf("SameSite = %v, want Strict", found.SameSite)
	}
	if !found.HttpOnly {
		t.Error("HttpOnly = false, want true")
	}
	if !found.Secure {
		t.Error("Secure = false, want true")
	}
	if found.Path != "/" {
		t.Errorf("Path = %q, want %q", found.Path, "/")
	}

	// Verify cookie value format: base64url.base64url
	if !strings.Contains(found.Value, ".") {
		t.Error("cookie value does not contain '.' separator")
	}
}

func TestGuest_ValidCookieAccepted(t *testing.T) {
	mw := GuestSession(testKey)
	var innerCalled bool
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		innerCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	// First request to get a cookie.
	req1 := httptest.NewRequest("GET", "/", nil)
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)

	var cookie *http.Cookie
	for _, c := range rec1.Result().Cookies() {
		if c.Name == guestCookieName {
			cookie = c
			break
		}
	}
	if cookie == nil {
		t.Fatal("no guest cookie from first request")
	}

	// Second request with the valid cookie — should NOT re-issue.
	req2 := httptest.NewRequest("GET", "/", nil)
	req2.AddCookie(cookie)
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	if !innerCalled {
		t.Fatal("inner handler not called")
	}
	if rec2.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec2.Code)
	}

	// Should not set a new cookie.
	for _, c := range rec2.Result().Cookies() {
		if c.Name == guestCookieName {
			t.Error("cookie re-issued on valid existing cookie")
		}
	}
}

func TestGuest_TamperedCookieRejected(t *testing.T) {
	mw := GuestSession(testKey)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// First request to get a cookie.
	req1 := httptest.NewRequest("GET", "/", nil)
	rec1 := httptest.NewRecorder()
	handler.ServeHTTP(rec1, req1)

	var cookie *http.Cookie
	for _, c := range rec1.Result().Cookies() {
		if c.Name == guestCookieName {
			cookie = c
			break
		}
	}
	if cookie == nil {
		t.Fatal("no guest cookie from first request")
	}

	// Tamper: flip a byte in the MAC portion.
	val := cookie.Value
	parts := strings.SplitN(val, ".", 2)
	mac := []byte(parts[1])
	if mac[0] == 'A' {
		mac[0] = 'B'
	} else {
		mac[0] = 'A'
	}
	tamperedValue := parts[0] + "." + string(mac)

	req2 := httptest.NewRequest("GET", "/", nil)
	req2.AddCookie(&http.Cookie{Name: guestCookieName, Value: tamperedValue})
	rec2 := httptest.NewRecorder()
	handler.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec2.Code)
	}

	// A new cookie should be issued to replace the tampered one.
	var newCookie *http.Cookie
	for _, c := range rec2.Result().Cookies() {
		if c.Name == guestCookieName {
			newCookie = c
			break
		}
	}
	if newCookie == nil {
		t.Error("new cookie not issued after tampered cookie")
	}
}

func TestGuest_MissingCookieRejectedOnAPIPaths(t *testing.T) {
	// GuestSessionID should return false for missing cookie.
	req := httptest.NewRequest("GET", "/api/test", nil)
	_, ok := GuestSessionID(req, testKey)
	if ok {
		t.Error("GuestSessionID() = true for missing cookie, want false")
	}
}

func TestGuest_GuestSessionID_ValidCookie(t *testing.T) {
	// Generate a valid cookie value.
	id, err := generateGuestID()
	if err != nil {
		t.Fatalf("generateGuestID error: %v", err)
	}
	value := signGuestCookie(id, DeriveKey(testKey))

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: guestCookieName, Value: value})

	sessionID, ok := GuestSessionID(req, testKey)
	if !ok {
		t.Fatal("GuestSessionID() = false for valid cookie")
	}
	if sessionID == "" {
		t.Error("session ID is empty")
	}
}

func TestGuest_GuestSessionID_TamperedCookie(t *testing.T) {
	// Create a valid cookie then tamper with it.
	id, err := generateGuestID()
	if err != nil {
		t.Fatalf("generateGuestID error: %v", err)
	}
	value := signGuestCookie(id, DeriveKey(testKey))

	// Tamper: modify the ID portion.
	parts := strings.SplitN(value, ".", 2)
	idBytes := []byte(parts[0])
	if idBytes[0] == 'A' {
		idBytes[0] = 'B'
	} else {
		idBytes[0] = 'A'
	}
	tamperedValue := string(idBytes) + "." + parts[1]

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&http.Cookie{Name: guestCookieName, Value: tamperedValue})

	_, ok := GuestSessionID(req, testKey)
	if ok {
		t.Error("GuestSessionID() = true for tampered cookie, want false")
	}
}

func TestGuest_VerifyCookie_InvalidBase64(t *testing.T) {
	_, ok := verifyGuestCookie("!!!invalid.!!!invalid", DeriveKey(testKey))
	if ok {
		t.Error("verifyGuestCookie should reject invalid base64")
	}
}

func TestGuest_VerifyCookie_NoDot(t *testing.T) {
	_, ok := verifyGuestCookie("nodothere", DeriveKey(testKey))
	if ok {
		t.Error("verifyGuestCookie should reject cookie without dot separator")
	}
}
