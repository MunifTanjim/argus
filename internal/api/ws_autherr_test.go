package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDialAuthErrorNoTokenSent(t *testing.T) {
	authorize := func(token string) bool { return token == "secret" }
	ts := httptest.NewServer(echoServer().WSHandler(authorize))
	defer ts.Close()

	_, err := DialWS(context.Background(), wsURL(ts.URL), "", nil)
	var authErr *DialAuthError
	if !errors.As(err, &authErr) {
		t.Fatalf("want *DialAuthError, got %T: %v", err, err)
	}
	if authErr.StatusCode != http.StatusUnauthorized {
		t.Errorf("StatusCode = %d, want %d", authErr.StatusCode, http.StatusUnauthorized)
	}
	if authErr.TokenSent {
		t.Error("TokenSent = true, want false when the caller passed no token")
	}
	if !strings.Contains(authErr.Error(), "no bearer token was sent") {
		t.Errorf("message does not name the missing token: %q", authErr.Error())
	}
}

func TestDialAuthErrorTokenRejected(t *testing.T) {
	authorize := func(token string) bool { return token == "secret" }
	ts := httptest.NewServer(echoServer().WSHandler(authorize))
	defer ts.Close()

	_, err := DialWS(context.Background(), wsURL(ts.URL), "wrong", nil)
	var authErr *DialAuthError
	if !errors.As(err, &authErr) {
		t.Fatalf("want *DialAuthError, got %T: %v", err, err)
	}
	if !authErr.TokenSent {
		t.Error("TokenSent = false, want true when the caller passed a token")
	}
	if strings.Contains(authErr.Error(), "wrong") {
		t.Errorf("message leaks the token: %q", authErr.Error())
	}
	if !strings.Contains(authErr.Error(), ts.URL[len("http://"):]) {
		t.Errorf("message does not name the gateway: %q", authErr.Error())
	}
}

// A non-auth handshake failure keeps the underlying error untouched: only 401 and
// 403 mean the caller's token is the problem.
func TestDialNonAuthRejectionIsNotAuthError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer ts.Close()

	_, err := DialWS(context.Background(), wsURL(ts.URL), "tok", nil)
	if err == nil {
		t.Fatal("want handshake failure")
	}
	var authErr *DialAuthError
	if errors.As(err, &authErr) {
		t.Fatalf("500 must not become *DialAuthError: %v", err)
	}
}
