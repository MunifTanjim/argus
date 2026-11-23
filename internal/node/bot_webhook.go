package node

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/MunifTanjim/argus/internal/bot"
	"github.com/MunifTanjim/argus/internal/bot/routine"
)

const (
	startedByEvent = "event"
	// maxWebhookBody is the largest webhook body accepted.
	maxWebhookBody = 64 << 10
	webhookPrefix  = "/hooks/"
)

func hashWebhookSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// rotateWebhookSecret makes a new bearer secret for the routine's webhook,
// stores its hash and returns the secret, which is not kept.
func (d *Node) rotateWebhookSecret(ctx context.Context, botID, slug string) (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	secret := "whs_" + hex.EncodeToString(b)
	if err := d.bots.SetWebhookSecretHash(ctx, botID, slug, hashWebhookSecret(secret)); err != nil {
		return "", err
	}
	return secret, nil
}

// webhookStart reports whether r is started by a webhook.
func webhookStart(r routine.Routine) bool {
	for _, s := range r.Starts {
		if s.Event == "webhook" {
			return true
		}
	}
	return false
}

// serveWebhooks serves the routine webhook listener when one is configured.
// The returned func stops it.
func (d *Node) serveWebhooks(ctx context.Context) (func(), error) {
	addr := d.botCfg.WebhookListen
	if d.bots == nil || addr == "" {
		return func() {}, nil
	}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("webhook listener: %w", err)
	}
	srv := &http.Server{Handler: d.webhookHandler(ctx), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(l) }()
	d.log.Info("serving routine webhooks", "addr", l.Addr().String())
	return func() { _ = srv.Close() }, nil
}

// webhookHandler accepts POST /hooks/<bot>/<slug>. Runs it starts live on
// runCtx, not on the request.
func (d *Node) webhookHandler(runCtx context.Context) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		status, msg := d.webhook(runCtx, req)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": msg})
	})
}

func (d *Node) webhook(runCtx context.Context, req *http.Request) (int, string) {
	if req.Method != http.MethodPost {
		return http.StatusMethodNotAllowed, "POST only"
	}
	name, slug, ok := strings.Cut(strings.TrimPrefix(req.URL.Path, webhookPrefix), "/")
	if !strings.HasPrefix(req.URL.Path, webhookPrefix) || !ok || name == "" || !routine.ValidSlug(slug) {
		return http.StatusNotFound, "not found"
	}
	ctx := req.Context()
	a, found, err := d.bots.BotByName(ctx, name)
	if err != nil {
		return http.StatusInternalServerError, "error"
	}
	if !found || !a.ArchivedAt.IsZero() {
		return http.StatusNotFound, "not found"
	}
	hash, found, err := d.bots.WebhookSecretHash(ctx, a.ID, slug)
	if err != nil {
		return http.StatusInternalServerError, "error"
	}
	if !found {
		return http.StatusNotFound, "not found"
	}
	token, bearer := strings.CutPrefix(req.Header.Get("Authorization"), "Bearer ")
	if !bearer || subtle.ConstantTimeCompare([]byte(hashWebhookSecret(strings.TrimSpace(token))), []byte(hash)) != 1 {
		return http.StatusUnauthorized, "unauthorized"
	}
	data, err := os.ReadFile(filepath.Join(a.Dir, routinesDir, slug+".yaml"))
	if err != nil {
		return http.StatusNotFound, "not found"
	}
	r, err := routine.Parse(slug, data)
	if err != nil || !webhookStart(r) {
		return http.StatusNotFound, "not found"
	}
	body, err := io.ReadAll(http.MaxBytesReader(nil, req.Body, maxWebhookBody))
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		return http.StatusRequestEntityTooLarge, fmt.Sprintf("body over %d KB", maxWebhookBody>>10)
	case err != nil:
		return http.StatusBadRequest, "unreadable body"
	case len(strings.TrimSpace(string(body))) > 0 && !json.Valid(body):
		return http.StatusBadRequest, "body must be JSON"
	}

	set := d.botSettings(a)
	if err := routine.Validate(r, routine.Limits{MinInterval: set.Limits.Limits.MinInterval}); err != nil || !r.Enabled {
		return http.StatusConflict, "routine is not enabled"
	}
	if st, ok := d.routineState(ctx, a.ID, slug); ok && !st.Enabled {
		return http.StatusConflict, "routine is paused"
	}
	if limit := set.Limits.Limits.MaxRunsPerHour; limit > 0 {
		n, err := d.bots.RunsLastHour(ctx, a.ID)
		if err != nil {
			return http.StatusInternalServerError, "error"
		}
		if n >= limit {
			return http.StatusTooManyRequests, fmt.Sprintf("max-runs-per-hour (%d) reached", limit)
		}
	}
	occ := d.botNow()
	payload := string(body)
	accepted := ""
	d.dispatchOccurrence(runCtx, a, set, r, occ, func() (bot.RoutineRun, bool, error) {
		run, ok, err := d.bots.ClaimOccurrence(runCtx, a.ID, slug, occ, startedByEvent, false)
		if ok {
			accepted = run.ID
			d.routinePayloads.Store(run.ID, payload)
		}
		return run, ok, err
	})
	if accepted == "" {
		return http.StatusInternalServerError, "not queued"
	}
	if run, ok, _ := d.bots.RoutineRun(ctx, accepted); ok && run.State == bot.RunSkipped {
		d.routinePayloads.Delete(accepted)
		return http.StatusOK, "accepted; skipped: " + run.Error
	}
	return http.StatusOK, "accepted"
}
