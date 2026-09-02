package main

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
)

func TestGatewayAuthHintMissingToken(t *testing.T) {
	err := &api.DialAuthError{URL: "ws://gw:8443/client", StatusCode: http.StatusUnauthorized}
	hint := gatewayAuthHint(err)
	for _, want := range []string{"--token", "ARGUS_TOKEN", "token:"} {
		if !strings.Contains(hint, want) {
			t.Errorf("hint omits %q: %q", want, hint)
		}
	}
}

func TestGatewayAuthHintWrongTokenSaysItDiffers(t *testing.T) {
	err := &api.DialAuthError{URL: "ws://gw:8443/client", StatusCode: http.StatusUnauthorized, TokenSent: true}
	hint := gatewayAuthHint(err)
	if !strings.Contains(hint, "different token") {
		t.Errorf("hint should say the gateway wants a different token: %q", hint)
	}
}

func TestGatewayAuthHintSilentOnOtherErrors(t *testing.T) {
	if hint := gatewayAuthHint(errors.New("connection refused")); hint != "" {
		t.Errorf("want no hint for a non-auth error, got %q", hint)
	}
}

// The hint must survive wrapping: fetchRoster and friends wrap dial errors.
func TestGatewayAuthHintThroughWrappedError(t *testing.T) {
	wrapped := errors.Join(errors.New("nodes.list"), &api.DialAuthError{URL: "ws://gw:8443/client", StatusCode: http.StatusForbidden})
	if gatewayAuthHint(wrapped) == "" {
		t.Error("want a hint for a wrapped *api.DialAuthError")
	}
}
