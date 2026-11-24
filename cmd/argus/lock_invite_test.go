package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/client"
	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/invite"
	"github.com/MunifTanjim/argus/internal/trustpin"
)

func testBlob() invite.Blob {
	return invite.Blob{
		GatewayURL: "wss://gw.example",
		Token:      "tok",
		Genesis:    testGenesis(0x41),
		IssuerID:   "issuer",
		IssuerKey:  bytes.Repeat([]byte{0x01}, 32),
		InviteID:   bytes.Repeat([]byte{0x02}, 16),
		Secret:     bytes.Repeat([]byte{0x03}, 32),
	}
}

// acceptFixture is an accept run whose redeem and wait steps are fakes.
type acceptFixture struct {
	cfgPath    string
	redeemed   bool
	redeemErr  error
	authorized bool
	extra      [][]byte
}

func newAcceptFixture(t *testing.T, cfgYAML string) *acceptFixture {
	t.Helper()
	tempStateDir(t)
	f := &acceptFixture{cfgPath: filepath.Join(t.TempDir(), "config.yaml"), authorized: true}
	if cfgYAML != "" {
		if err := os.WriteFile(f.cfgPath, []byte(cfgYAML), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *acceptFixture) run(cfg *config.Config, force bool) (string, error) {
	return acceptInvite(context.Background(), acceptEnv{
		cfg:        cfg,
		configPath: f.cfgPath,
		redeem: func(_ context.Context, _ e2e.KeyPair, extra [][]byte) error {
			f.redeemed = true
			f.extra = extra
			return f.redeemErr
		},
		waitAuthorized: func(context.Context, e2e.KeyPair, []byte) error {
			if !f.authorized {
				return errors.New("not authorized yet")
			}
			return nil
		},
	}, testBlob(), force)
}

func wantNoPins(t *testing.T, why string) {
	t.Helper()
	for _, pf := range []*trustpin.File{nodePinFile(), clientPinFile()} {
		if g, _ := pf.Load(); g != nil {
			t.Errorf("a pin was written despite %s: %x", why, g)
		}
	}
}

func TestAcceptHappyPath(t *testing.T) {
	f := newAcceptFixture(t, "")
	out, err := f.run(&config.Config{}, false)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if !strings.Contains(out, "argus start") {
		t.Errorf("success text must tell the user to run argus start, got %q", out)
	}
	data, _ := os.ReadFile(f.cfgPath)
	for _, want := range []string{"wss://gw.example", "tok", "enabled: true"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("config missing %q:\n%s", want, data)
		}
	}
	for _, pf := range []*trustpin.File{nodePinFile(), clientPinFile()} {
		if g, _ := pf.Load(); !bytes.Equal(g, testBlob().Genesis) {
			t.Errorf("pin = %x, want blob genesis", g)
		}
	}
	clientID, err := e2e.LoadOrCreateIdentity(config.GetStatePath("client-identity.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.extra) != 1 || !bytes.Equal(f.extra[0], clientID.Public) {
		t.Errorf("extra keys = %x, want the client identity", f.extra)
	}
}

func TestAcceptConfigConflictStops(t *testing.T) {
	f := newAcceptFixture(t, "gateway:\n  url: wss://other.example\n")
	_, err := f.run(&config.Config{}, false)
	if err == nil {
		t.Fatal("accept must fail on a conflicting gateway.url")
	}
	for _, want := range []string{"gateway.url", "--force"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q must mention %q", err, want)
		}
	}
	wantNoPins(t, "the config conflict")
	if f.redeemed {
		t.Error("redeem ran despite the config conflict")
	}
}

func TestAcceptForceOverwritesConfig(t *testing.T) {
	f := newAcceptFixture(t, "gateway:\n  url: wss://other.example\n")
	if _, err := f.run(&config.Config{}, true); err != nil {
		t.Fatalf("accept --force: %v", err)
	}
	data, _ := os.ReadFile(f.cfgPath)
	if !strings.Contains(string(data), "wss://gw.example") {
		t.Errorf("--force did not overwrite gateway.url:\n%s", data)
	}
}

func TestAcceptPinConflictStops(t *testing.T) {
	const yaml = "# keep me\ntoken: tok\n"
	f := newAcceptFixture(t, yaml)
	other := testGenesis(0x99)
	if err := clientPinFile().Save(other); err != nil {
		t.Fatal(err)
	}
	_, err := f.run(&config.Config{}, true)
	if err == nil || err.Error() != existingPinConflict(other).Error() {
		t.Fatalf("err = %v, want existingPinConflict", err)
	}
	if data, _ := os.ReadFile(f.cfgPath); string(data) != yaml {
		t.Errorf("config changed despite the pin conflict:\n%s", data)
	}
	if f.redeemed {
		t.Error("redeem ran despite the pin conflict")
	}
}

func TestAcceptConfigGenesisConflictStops(t *testing.T) {
	f := newAcceptFixture(t, "")
	other := testGenesis(0x98)
	cfg := &config.Config{Lock: config.LockConfig{Genesis: trustpin.Encode(other)}}
	_, err := f.run(cfg, true)
	if err == nil || err.Error() != configPinConflict(other).Error() {
		t.Fatalf("err = %v, want configPinConflict", err)
	}
	if _, serr := os.Stat(f.cfgPath); !os.IsNotExist(serr) {
		t.Error("config written despite the lock.genesis conflict")
	}
}

func TestAcceptMayHaveAuthorized(t *testing.T) {
	used := &api.RPCError{Code: api.CodeInvalidRequest, Message: invite.ErrUsed.Error()}
	for _, tc := range []struct {
		name       string
		redeemErr  error
		authorized bool
		want       []string // error substrings; none means success
	}{
		{"used, authorized", used, true, nil},
		{"lost, authorized", fmt.Errorf("%w: gateway connection closed", client.ErrRedeemResponseLost), true, nil},
		{"used, not authorized", used, false, []string{invite.ErrUsed.Error()}},
		{"lost, not authorized", fmt.Errorf("%w: the issuer did not answer the redeem request", client.ErrRedeemResponseLost), false,
			[]string{"did not answer", "the invite may have been used; re-run accept with the same invite to check"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAcceptFixture(t, "")
			f.redeemErr, f.authorized = tc.redeemErr, tc.authorized
			out, err := f.run(&config.Config{}, false)
			if tc.want == nil {
				if err != nil {
					t.Fatalf("accept: %v", err)
				}
				if !strings.Contains(out, "argus start") {
					t.Errorf("want the success text, got %q", out)
				}
				return
			}
			if err == nil {
				t.Fatal("accept must fail")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q must mention %q", err, want)
				}
			}
		})
	}
}

func TestAcceptRedeemFailureMentionsPin(t *testing.T) {
	const hint = "now points at the invite's gateway, and this machine is pinned to its network"
	for name, setup := range map[string]func(*acceptFixture){
		"error": func(f *acceptFixture) { f.redeemErr = errors.New("boom") },
		"used, not authorized": func(f *acceptFixture) {
			f.redeemErr = &api.RPCError{Code: api.CodeInvalidRequest, Message: invite.ErrUsed.Error()}
			f.authorized = false
		},
		"lost, not authorized": func(f *acceptFixture) {
			f.redeemErr = fmt.Errorf("%w: gateway connection closed", client.ErrRedeemResponseLost)
			f.authorized = false
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newAcceptFixture(t, "")
			setup(f)
			_, err := f.run(&config.Config{}, false)
			if err == nil || !strings.Contains(err.Error(), hint) {
				t.Fatalf("err = %v, want the unpin hint", err)
			}
		})
	}
}

// isolateCLI points the state dir, config dir and local node socket at temp
// paths, so a root-command test never reaches the user's real config or node.
func isolateCLI(t *testing.T) (sock string) {
	t.Helper()
	tempStateDir(t)
	oldConfigDir := config.ConfigDir
	config.ConfigDir = t.TempDir()
	t.Cleanup(func() { config.ConfigDir = oldConfigDir })
	t.Setenv("ARGUS_CONFIG", "")
	sock = shortSocket(t)
	t.Setenv("ARGUS_SOCKET", sock)
	return sock
}

func TestIsolateCLIUsesTempSocket(t *testing.T) {
	sock := isolateCLI(t)
	root := newRootCmd("test")
	root.SetArgs([]string{"lock", "invite", "--url", "wss://gw.example"})
	var err error
	_, stderr := captureOutput(t, func() { err = root.Execute() })
	if err == nil || !strings.Contains(stderr, sock) {
		t.Fatalf("err = %v, stderr = %q; want a dial error naming %s", err, stderr, sock)
	}
}

func TestInviteCmdNeedsURL(t *testing.T) {
	isolateCLI(t)
	t.Setenv("ARGUS_GATEWAY_URL", "")
	root := newRootCmd("test")
	root.SetArgs([]string{"--no-config", "lock", "invite"})
	var err error
	_, stderr := captureOutput(t, func() { err = root.Execute() })
	if err == nil {
		t.Fatal("lock invite without a gateway URL must fail")
	}
	if !strings.Contains(stderr, "pass --url") {
		t.Errorf("stderr = %q, want it to say pass --url", stderr)
	}
}

func TestInviteCmdRejectsLoopbackGatewayURL(t *testing.T) {
	isolateCLI(t)
	t.Setenv("ARGUS_GATEWAY_URL", "ws://127.0.0.1:7777")
	root := newRootCmd("test")
	root.SetArgs([]string{"--no-config", "lock", "invite"})
	var err error
	_, stderr := captureOutput(t, func() { err = root.Execute() })
	if err == nil || !strings.Contains(stderr, "only reachable from this machine") {
		t.Fatalf("err = %v, stderr = %q; want a loopback refusal", err, stderr)
	}
}

func TestIsLoopbackURL(t *testing.T) {
	for raw, want := range map[string]bool{
		"ws://localhost:7777":     true,
		"wss://LOCALHOST":         true,
		"ws://gw.localhost":       true,
		"ws://127.0.0.1:7777":     true,
		"ws://127.8.0.1":          true,
		"ws://[::1]:7777":         true,
		"wss://gw.example":        false,
		"ws://192.168.1.5:7777":   false,
		"wss://localhost.example": false,
	} {
		if got := isLoopbackURL(raw); got != want {
			t.Errorf("isLoopbackURL(%q) = %v, want %v", raw, got, want)
		}
	}
}

func TestInviteCmdRejectsSubSecondTTL(t *testing.T) {
	isolateCLI(t)
	root := newRootCmd("test")
	root.SetArgs([]string{"--no-config", "lock", "invite", "--url", "wss://gw.example", "--ttl", "500ms"})
	var err error
	_, stderr := captureOutput(t, func() { err = root.Execute() })
	if err == nil || !strings.Contains(stderr, "--ttl must be from 1s") {
		t.Fatalf("err = %v, stderr = %q; want --ttl must be from 1s", err, stderr)
	}
}

func TestInviteTTLSeconds(t *testing.T) {
	for _, tc := range []struct {
		ttl  time.Duration
		want int64
	}{
		{time.Second, 1},
		{90*time.Second + 900*time.Millisecond, 91},
		{90*time.Second + time.Nanosecond, 91},
		{time.Hour, 3600},
		{invite.MaxTTL, 2592000},
	} {
		got, err := inviteTTLSeconds(tc.ttl)
		if err != nil || got != tc.want {
			t.Errorf("inviteTTLSeconds(%s) = %d, %v; want %d", tc.ttl, got, err, tc.want)
		}
	}
	for _, ttl := range []time.Duration{0, -time.Second, 999 * time.Millisecond, invite.MaxTTL + time.Second} {
		if _, err := inviteTTLSeconds(ttl); err == nil || !strings.HasPrefix(err.Error(), "--ttl must be from 1s") {
			t.Errorf("inviteTTLSeconds(%s) err = %v, want --ttl must be from 1s", ttl, err)
		}
	}
}

func TestAcceptCmdRejectsNoConfig(t *testing.T) {
	isolateCLI(t)
	blob, err := testBlob().Encode()
	if err != nil {
		t.Fatal(err)
	}
	root := newRootCmd("test")
	root.SetArgs([]string{"--no-config", "lock", "invite", "accept", blob})
	_, stderr := captureOutput(t, func() { err = root.Execute() })
	if err == nil || !strings.Contains(stderr, "accept writes the config file; drop --no-config") {
		t.Fatalf("err = %v, stderr = %q; want the --no-config refusal", err, stderr)
	}
	wantNoPins(t, "--no-config")
	if _, serr := os.Stat(config.GetConfigPath("config.yaml")); !os.IsNotExist(serr) {
		t.Error("config written despite --no-config")
	}
}

func TestAcceptEnvOverrideWarnings(t *testing.T) {
	b := testBlob()
	t.Setenv("ARGUS_GATEWAY_URL", "")
	t.Setenv("ARGUS_TOKEN", "")
	if got := acceptEnvWarnings(b); len(got) != 0 {
		t.Errorf("unset env: warnings = %q, want none", got)
	}
	t.Setenv("ARGUS_GATEWAY_URL", b.GatewayURL)
	t.Setenv("ARGUS_TOKEN", b.Token)
	if got := acceptEnvWarnings(b); len(got) != 0 {
		t.Errorf("matching env: warnings = %q, want none", got)
	}
	t.Setenv("ARGUS_GATEWAY_URL", "wss://other.example")
	t.Setenv("ARGUS_TOKEN", "other")
	got := acceptEnvWarnings(b)
	if len(got) != 2 {
		t.Fatalf("differing env: warnings = %q, want two", got)
	}
	for i, want := range [][]string{{"ARGUS_GATEWAY_URL", "gateway.url"}, {"ARGUS_TOKEN", "token"}} {
		for _, w := range want {
			if !strings.Contains(got[i], w) || !strings.Contains(got[i], "overrides") {
				t.Errorf("warning %q must mention %q and that it overrides the config file", got[i], w)
			}
		}
	}
	b.Token = ""
	if got := acceptEnvWarnings(b); len(got) != 1 {
		t.Errorf("tokenless invite: warnings = %q, want only the gateway.url one", got)
	}
}

func TestInviteRevokeRejectsBadHex(t *testing.T) {
	isolateCLI(t)
	root := newRootCmd("test")
	root.SetArgs([]string{"--no-config", "lock", "invite", "revoke", "zz"})
	var err error
	_, stderr := captureOutput(t, func() { err = root.Execute() })
	if err == nil || !strings.Contains(stderr, "invite id") {
		t.Fatalf("err = %v, stderr = %q; want an invite id error", err, stderr)
	}
}

func TestInviteListText(t *testing.T) {
	got := inviteListText([]api.LockInviteInfo{
		{ID: []byte{0xab, 0xcd}, State: "pending"},
		{ID: []byte{0x01}, State: "used", Keys: [][]byte{bytes.Repeat([]byte{0x05}, 32)}},
	})
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 2 {
		t.Fatalf("want one line per invite, got %q", got)
	}
	if !strings.HasPrefix(lines[0], "abcd") || !strings.Contains(lines[0], "pending") {
		t.Errorf("line 0 = %q", lines[0])
	}
	if !strings.Contains(lines[1], "used") || !strings.Contains(lines[1], "devpub:") {
		t.Errorf("line 1 = %q, want state and authorized keys", lines[1])
	}
	if inviteListText(nil) == "" {
		t.Error("an empty list must still say something")
	}
}

func TestAcceptIssuerCannotAuthorize(t *testing.T) {
	for _, msg := range []string{
		api.ErrMsgLockedModeOff,
		api.ErrMsgNotSigner,
		api.ErrMsgNoSignerKey,
		api.ErrMsgInvitesOff,
	} {
		f := newAcceptFixture(t, "")
		f.redeemErr = &api.RPCError{Code: api.CodeInvalidRequest, Message: msg}
		_, err := f.run(&config.Config{}, false)
		if err == nil {
			t.Fatalf("%q: accept must fail", msg)
		}
		for _, want := range []string{"issuing node can no longer authorize devices", msg, "ask for an invite from a current signer node"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q must mention %q", err, want)
			}
		}
	}
}

func TestRedeemFailureIssuerUnreachableHint(t *testing.T) {
	err := redeemFailure(client.ErrIssuerUnreachable)
	for _, want := range []string{"or the invite no longer exists", "make sure the issuing signer node is running"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q must mention %q", err, want)
		}
	}
}

func TestRedeemFailureInviteMismatch(t *testing.T) {
	err := redeemFailure(fmt.Errorf("%w: e2e: read msg2: noise: message auth failed", client.ErrInviteMismatch))
	if want := "redeem invite: " + client.ErrInviteMismatch.Error(); err.Error() != want {
		t.Fatalf("err = %q, want %q", err, want)
	}
}

func TestAcceptReportsProgress(t *testing.T) {
	f := newAcceptFixture(t, "")
	f.redeemErr = &api.RPCError{Code: api.CodeInvalidRequest, Message: invite.ErrUsed.Error()}
	var msgs []string
	_, err := acceptInvite(context.Background(), acceptEnv{
		cfg:            &config.Config{},
		configPath:     f.cfgPath,
		redeem:         func(context.Context, e2e.KeyPair, [][]byte) error { return f.redeemErr },
		waitAuthorized: func(context.Context, e2e.KeyPair, []byte) error { return nil },
		progress:       func(m string) { msgs = append(msgs, m) },
	}, testBlob(), false)
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if len(msgs) != 2 || !strings.Contains(msgs[1], "waiting for the network's trust log") {
		t.Fatalf("progress = %q, want the redeem and wait steps", msgs)
	}
}

func TestRedeemOverGatewayDialFailure(t *testing.T) {
	var deadline time.Time
	dial := func(ctx context.Context) (net.Conn, error) {
		deadline, _ = ctx.Deadline()
		return nil, errors.New("connection refused")
	}
	node, err := e2e.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	err = redeemOverGateway(context.Background(), dial, node, testBlob(), nil)
	limit := time.Now().Add(client.InviteRedeemTimeout())
	if err == nil || !strings.Contains(err.Error(), "gateway unreachable: wss://gw.example") {
		t.Fatalf("err = %v, want gateway unreachable", err)
	}
	if deadline.IsZero() || deadline.After(limit) {
		t.Errorf("dial deadline = %v, want one within %s", deadline, client.InviteRedeemTimeout())
	}
}
