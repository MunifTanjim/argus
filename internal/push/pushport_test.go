package push

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func hostOf(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host
}

func TestPostToPushPortSetsBearerAndSucceedsOn202(t *testing.T) {
	var gotAuth, gotEnc string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotEnc = r.Header.Get("Content-Encoding")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	err := PostToPushPort(context.Background(), srv.Client(), "pit_tok", srv.URL+"/push/x", []byte("body"), "", "")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if gotAuth != "Bearer pit_tok" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotEnc != "aes128gcm" {
		t.Errorf("Content-Encoding = %q", gotEnc)
	}
}

func TestPostToPushPortGoneOn410(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusGone)
	}))
	defer srv.Close()
	err := PostToPushPort(context.Background(), srv.Client(), "pit_tok", srv.URL, []byte("b"), "", "")
	if !errors.Is(err, ErrGone) {
		t.Fatalf("want ErrGone, got %v", err)
	}
}

func TestPostToPushPortConfigErrorDoesNotPrune(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(code)
		}))
		err := PostToPushPort(context.Background(), srv.Client(), "pit_tok", srv.URL, []byte("b"), "", "")
		if err == nil {
			t.Errorf("code %d: want error", code)
		}
		if errors.Is(err, ErrGone) {
			t.Errorf("code %d: must not be ErrGone (no prune)", code)
		}
		srv.Close()
	}
}

func TestGatewayDelivererSetPushPortLiveUpdate(t *testing.T) {
	var gotAuth string
	pp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer pp.Close()

	d := NewGatewayDeliverer(nil, nil)
	// Before SetPushPort: PushPort host with no token -> VAPID path (nil vapid -> no auth header)
	if err := d.Deliver(context.Background(), pp.URL+"/x", []byte("b"), "", ""); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "" {
		t.Fatalf("pre-set Authorization = %q, want empty", gotAuth)
	}
	d.SetPushPort(hostOf(t, pp.URL), "pit_live")
	if err := d.Deliver(context.Background(), pp.URL+"/x", []byte("b"), "", ""); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer pit_live" {
		t.Fatalf("post-set Authorization = %q, want Bearer pit_live", gotAuth)
	}
}

func TestGatewayDelivererHasPushPort(t *testing.T) {
	d := NewGatewayDeliverer(nil, nil)
	if d.HasPushPort() {
		t.Fatal("HasPushPort should be false initially")
	}
	d.SetPushPort("push.example", "pit_x")
	if !d.HasPushPort() {
		t.Fatal("HasPushPort should be true after SetPushPort with a token")
	}
	d.SetPushPort("push.example", "")
	if d.HasPushPort() {
		t.Fatal("HasPushPort should be false after clearing the token")
	}
}

func TestGatewayDelivererRoutesByHost(t *testing.T) {
	var pushportAuth, vapidPath string
	pp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pushportAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusAccepted)
	}))
	defer pp.Close()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		vapidPath = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusCreated)
	}))
	defer other.Close()

	d := NewGatewayDeliverer(nil, &PushPortAuth{Host: hostOf(t, pp.URL), Token: "pit_tok"})
	if err := d.Deliver(context.Background(), pp.URL+"/push/x", []byte("b"), "", ""); err != nil {
		t.Fatal(err)
	}
	if pushportAuth != "Bearer pit_tok" {
		t.Errorf("pushport Authorization = %q", pushportAuth)
	}
	if err := d.Deliver(context.Background(), other.URL+"/ep", []byte("b"), "", ""); err != nil {
		t.Fatal(err)
	}
	if vapidPath != "" {
		t.Errorf("non-pushport host got Authorization %q (vapid nil should omit)", vapidPath)
	}
}
