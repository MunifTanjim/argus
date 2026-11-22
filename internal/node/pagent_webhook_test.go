package node

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/pagent"
	"github.com/spf13/viper"
)

const hookBody = "what: triage the alert\nstarts:\n  - event: webhook\n"

// hookRig is a run rig with a webhook routine and its secret.
func hookRig(t *testing.T) (*runRig, http.Handler, string) {
	t.Helper()
	r := newRunRig(t)
	r.routine(t, "alert", hookBody)
	secret, err := r.d.rotateWebhookSecret(context.Background(), r.a.ID, "alert")
	if err != nil {
		t.Fatal(err)
	}
	return r, r.d.webhookHandler(context.Background()), secret
}

func post(h http.Handler, path, auth, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestWebhookAuth(t *testing.T) {
	r, h, secret := hookRig(t)
	path := "/hooks/" + r.a.Name + "/alert"
	for _, tc := range []struct {
		path, auth string
		want       int
	}{
		{path, "", http.StatusUnauthorized},
		{path, "Bearer wrong", http.StatusUnauthorized},
		{path, secret, http.StatusUnauthorized},
		{"/hooks/" + r.a.Name + "/nope", "Bearer " + secret, http.StatusNotFound},
		{"/hooks/nobody/alert", "Bearer " + secret, http.StatusNotFound},
	} {
		if w := post(h, tc.path, tc.auth, `{}`); w.Code != tc.want {
			t.Errorf("%s %q: %d, want %d", tc.path, tc.auth, w.Code, tc.want)
		}
	}
	if n := len(r.runs(t)); n != 0 {
		t.Fatalf("runs = %d", n)
	}
	// A rotated secret replaces the old one.
	newSecret, _ := r.d.rotateWebhookSecret(context.Background(), r.a.ID, "alert")
	if w := post(h, path, "Bearer "+secret, `{}`); w.Code != http.StatusUnauthorized {
		t.Fatalf("old secret: %d", w.Code)
	}
	if w := post(h, path, "Bearer "+newSecret, `{}`); w.Code != http.StatusOK {
		t.Fatalf("new secret: %d %s", w.Code, w.Body)
	}
	r.endAll(t)
}

func TestWebhookStartsRun(t *testing.T) {
	r, h, secret := hookRig(t)
	before := time.Now()
	w := post(h, "/hooks/"+r.a.Name+"/alert", "Bearer "+secret, `{"alert":"disk full"}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "accepted") {
		t.Fatalf("response: %d %s", w.Code, w.Body)
	}
	runs := r.runs(t)
	if len(runs) != 1 || runs[0].StartedBy != "event" || runs[0].Occurrence.Before(before.Add(-time.Second)) {
		t.Fatalf("runs: %+v", runs)
	}
	r.until(t, "spawned", func() bool { return r.lastPrompt() != "" })
	p := r.lastPrompt()
	if !strings.Contains(p, "<untrusted-data>\n{\"alert\":\"disk full\"}\n</untrusted-data>") || !strings.HasSuffix(p, "Run routine alert now.") {
		t.Fatalf("prompt: %q", p)
	}
	run, _, _ := r.store.RoutineRun(context.Background(), runs[0].ID)
	th, _, _ := r.store.Thread(context.Background(), run.ThreadID)
	if th.Kind != threadRun || th.StartedBy != "event" {
		t.Fatalf("thread: %+v", th)
	}
	r.endAll(t)

	// The runs-per-hour cap answers 429.
	r.d.pagentCfg.Defaults.Limits.MaxRunsPerHour = 1
	if w := post(h, "/hooks/"+r.a.Name+"/alert", "Bearer "+secret, `{}`); w.Code != http.StatusTooManyRequests {
		t.Fatalf("over the cap: %d %s", w.Code, w.Body)
	}
}

func TestWebhookTooLarge(t *testing.T) {
	r, h, secret := hookRig(t)
	big := `{"x":"` + strings.Repeat("a", maxWebhookBody) + `"}`
	if w := post(h, "/hooks/"+r.a.Name+"/alert", "Bearer "+secret, big); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("large body: %d", w.Code)
	}
	if w := post(h, "/hooks/"+r.a.Name+"/alert", "Bearer "+secret, "not json"); w.Code != http.StatusBadRequest {
		t.Fatalf("non-JSON body: %d", w.Code)
	}
	if n := len(r.runs(t)); n != 0 {
		t.Fatalf("runs = %d", n)
	}
}

func TestWebhookDisabledByDefault(t *testing.T) {
	t.Setenv("ARGUS_AGENTS_WEBHOOK_LISTEN_ADDR", "")
	v := viper.New()
	if err := config.Load(v, "", true); err != nil {
		t.Fatal(err)
	}
	if got := config.FromViper(v).Agents.WebhookListen; got != "" {
		t.Fatalf("listen-addr default = %q", got)
	}
	r := newRunRig(t)
	stop, err := r.d.serveWebhooks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	stop()

	r.d.pagentCfg.WebhookListen = "127.0.0.1:0"
	stop, err = r.d.serveWebhooks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	stop()
}

func (r *runRig) runs(t *testing.T) []pagent.RoutineRun {
	t.Helper()
	runs, err := r.store.RunsSince(context.Background(), r.a.ID, "alert", time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	return runs
}

// endAll ends every live run session and waits for the runs.
func (r *runRig) endAll(t *testing.T) {
	t.Helper()
	r.d.routineRuns.Range(func(_, v any) bool {
		c := v.(*runCtl)
		r.until(t, "spawned", func() bool { _, s := c.ids(); return s != "" })
		_, sid := c.ids()
		r.end(sid)
		return true
	})
	r.wait(t)
}
