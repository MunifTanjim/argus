package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/pagent"
	"github.com/MunifTanjim/argus/internal/session"
)

const (
	secretKeyFile       = "pagent-secrets.key"
	secretRequestPrefix = "secret:"
	secretSave          = "save"
	secretDecline       = "decline"
)

// secretState holds the secret store's key, opened on first use.
type secretState struct {
	mu   sync.Mutex
	path string // key file; empty means the data directory's
	box  *pagent.SecretBox
}

func (d *Node) secretBox() (*pagent.SecretBox, error) {
	d.secrets.mu.Lock()
	defer d.secrets.mu.Unlock()
	if d.secrets.box != nil {
		return d.secrets.box, nil
	}
	path := d.secrets.path
	if path == "" {
		path = config.GetDataPath(secretKeyFile)
	}
	b, err := pagent.OpenSecretBox(path)
	if err != nil {
		return nil, err
	}
	d.secrets.box = b
	return b, nil
}

// agentSecrets returns the agent's secrets with their values.
func (d *Node) agentSecrets(ctx context.Context, agentID string) []pagent.Secret {
	if d.pagents == nil || agentID == "" {
		return nil
	}
	list, err := d.pagents.Secrets(ctx, agentID)
	if err != nil || len(list) == 0 {
		return nil
	}
	b, err := d.secretBox()
	if err != nil {
		d.log.Warn("pagent: secret store", "err", err)
		return nil
	}
	vals, err := d.pagents.SecretValues(ctx, b, agentID)
	if err != nil {
		d.log.Warn("pagent: read secrets", "agent", agentID, "err", err)
	}
	return vals
}

// secretList returns the agent's secret names and descriptions.
func (d *Node) secretList(ctx context.Context, agentID string) []pagent.Secret {
	if d.pagents == nil {
		return nil
	}
	list, err := d.pagents.Secrets(ctx, agentID)
	if err != nil {
		d.log.Warn("pagent: list secrets", "agent", agentID, "err", err)
		return nil
	}
	return list
}

// redact replaces the agent's known secret values in text with «NAME».
func (d *Node) redact(ctx context.Context, agentID, text string) string {
	if text == "" {
		return text
	}
	return pagent.Redact(text, d.agentSecrets(ctx, agentID))
}

// redactJSON redacts a JSON document, keeping it valid JSON.
func (d *Node) redactJSON(ctx context.Context, agentID string, raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	out := json.RawMessage(d.redact(ctx, agentID, string(raw)))
	if json.Valid(out) {
		return out
	}
	q, _ := json.Marshal(string(out))
	return q
}

type secretEnvKey struct{}

// withAgentSecrets carries the agent's secrets as environment assignments to
// the session a spawn or resume starts.
func (d *Node) withAgentSecrets(ctx context.Context, agentID string) context.Context {
	secrets := d.agentSecrets(ctx, agentID)
	if len(secrets) == 0 {
		return ctx
	}
	env := make([]string, len(secrets))
	for i, s := range secrets {
		env[i] = s.Name + "=" + s.Value
	}
	return context.WithValue(ctx, secretEnvKey{}, env)
}

func secretEnvFrom(ctx context.Context) []string {
	env, _ := ctx.Value(secretEnvKey{}).([]string)
	return env
}

// secretEnvScript loads the assignment file "$0", removes it and runs the
// command. The values stay off command lines and out of the terminal
// multiplexer's environment.
const secretEnvScript = `. "$0"; rm -f "$0"; exec "$@"`

// withSecretEnv wraps a pane command so it starts with the secrets ctx carries.
// cleanup removes the assignment file when the pane did not start.
func withSecretEnv(ctx context.Context, command string, args []string) (string, []string, func(), error) {
	env := secretEnvFrom(ctx)
	if len(env) == 0 {
		return command, args, func() {}, nil
	}
	f, err := os.CreateTemp("", "argus-env-*")
	if err != nil {
		return "", nil, nil, err
	}
	var b strings.Builder
	for _, kv := range env {
		name, value, _ := strings.Cut(kv, "=")
		b.WriteString("export " + name + "=" + shellSingleQuote(value) + "\n")
	}
	_, werr := f.WriteString(b.String())
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(f.Name())
		return "", nil, nil, werr
	}
	wrapped := append([]string{"-c", secretEnvScript, f.Name(), command}, args...)
	return "sh", wrapped, func() { os.Remove(f.Name()) }, nil
}

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func (d *Node) handlePAgentSecretSet(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.PAgentSecretSetParams](params)
	if err != nil {
		return nil, err
	}
	s, err := d.pagentStore()
	if err != nil {
		return nil, err
	}
	a, err := d.pagentFind(ctx, s, p.AgentID)
	if err != nil {
		return nil, err
	}
	if err := d.storeSecret(ctx, a, p.Name, p.Description, p.Value); err != nil {
		return nil, err
	}
	return struct{}{}, nil
}

// reservedEnv are environment variables a secret can't replace: the shell,
// the dynamic loader, the terminal and Argus itself depend on them.
var reservedEnv = []string{
	"PATH", "HOME", "USER", "LOGNAME", "SHELL", "PWD", "OLDPWD", "TERM", "TMPDIR", "TZ",
	"LANG", "IFS", "ENV", "BASH_ENV", "CDPATH", "SHLVL", "PS1", "PS2", "PS4", "SSH_AUTH_SOCK",
}

var reservedEnvPrefixes = []string{"LC_", "LD_", "DYLD_", "XDG_", "TMUX", "ARGUS_"}

// secretNameReserved reports whether name is an environment variable a secret
// can't set.
func secretNameReserved(name string) bool {
	if slices.Contains(reservedEnv, name) {
		return true
	}
	for _, p := range reservedEnvPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// checkSecretName refuses names that are not like GH_TOKEN or that are
// reserved.
func checkSecretName(name string) error {
	if !pagent.ValidSecretName(name) {
		return pagentErr("secret name %q must be like GH_TOKEN: capital letters, digits and _", name)
	}
	if secretNameReserved(name) {
		return pagentErr("secret name %q is a reserved environment variable", name)
	}
	return nil
}

func (d *Node) storeSecret(ctx context.Context, a pagent.Agent, name, description, value string) error {
	if err := checkSecretName(name); err != nil {
		return err
	}
	if value == "" {
		return pagentErr("secret value is empty")
	}
	b, err := d.secretBox()
	if err != nil {
		return pagentErr("%v", err)
	}
	enc, err := b.Seal(a.ID, name, value)
	if err != nil {
		return pagentErr("%v", err)
	}
	if err := d.pagents.SetSecret(ctx, a.ID, name, strings.TrimSpace(description), enc); err != nil {
		return pagentErr("%v", err)
	}
	d.notifyConns(api.MethodPAgentChanged)
	return nil
}

func (d *Node) handlePAgentSecretList(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.PAgentRef](params)
	if err != nil {
		return nil, err
	}
	s, err := d.pagentStore()
	if err != nil {
		return nil, err
	}
	a, err := d.pagentFind(ctx, s, p.AgentID)
	if err != nil {
		return nil, err
	}
	list, err := s.Secrets(ctx, a.ID)
	if err != nil {
		return nil, pagentErr("%v", err)
	}
	res := api.PAgentSecretListResult{Secrets: make([]api.PAgentSecretInfo, len(list))}
	for i, sec := range list {
		res.Secrets[i] = api.PAgentSecretInfo{Name: sec.Name, Description: sec.Description, UpdatedAt: sec.UpdatedAt}
	}
	return res, nil
}

func (d *Node) handlePAgentSecretRm(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.PAgentSecretRef](params)
	if err != nil {
		return nil, err
	}
	s, err := d.pagentStore()
	if err != nil {
		return nil, err
	}
	a, err := d.pagentFind(ctx, s, p.AgentID)
	if err != nil {
		return nil, err
	}
	if err := s.DeleteSecret(ctx, a.ID, p.Name); errors.Is(err, pagent.ErrNotFound) {
		return nil, pagentErr("secret %s not found", p.Name)
	} else if err != nil {
		return nil, pagentErr("%v", err)
	}
	d.notifyConns(api.MethodPAgentChanged)
	return struct{}{}, nil
}

type requestSecretParams struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func isSecretRequest(requestID string) bool { return strings.HasPrefix(requestID, secretRequestPrefix) }

// mcpRequestSecret asks the user for a secret value. The value goes to the
// node's secret store; the agent only hears its variable name.
func (d *Node) mcpRequestSecret(ctx context.Context, caller callerRef, p requestSecretParams) (any, error) {
	a, th, err := d.pagentCaller(ctx, caller)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(p.Name)
	if err := checkSecretName(name); err != nil {
		return nil, err
	}
	if _, ok := d.reg.Get(caller.Local.ID); !ok {
		return nil, fmt.Errorf("session not found")
	}
	desc := strings.TrimSpace(p.Description)
	d.reg.PushDecision(caller.Local.ID, session.Interaction{
		Kind:       session.InteractionSecret,
		Message:    desc,
		SecretName: name,
		RequestID:  secretRequestPrefix + name,
		Options: []session.DecisionOption{
			{Label: "Save", Value: secretSave},
			{Label: "Decline", Value: secretDecline, Reject: true},
		},
	})
	d.pagentNotify(ctx, a, th.ID, NotifyNeedsYou, th.StartedBy, a.Name+" asks for a secret", name)
	return map[string]string{"status": "waiting for the user; you'll get a message when it is stored"}, nil
}

// respondSecret stores the value of a secret interaction and tells the agent
// the variable name only.
func (d *Node) respondSecret(ctx context.Context, s session.Session, rid string, p api.RespondParams) error {
	ix, ok := d.reg.Decision(s.ID, rid)
	if !ok {
		return errRequestGone
	}
	tagged := s
	tagged.PAgentID, tagged.PAgentThreadID = d.pagentTag(s)
	a, _, err := d.pagentCaller(ctx, callerRef{Local: &tagged})
	if err != nil {
		return err
	}
	name := ix.SecretName
	var msg string
	switch {
	case p.OptionValue == secretDecline || p.Behavior == "deny":
		msg = "The user declined to give " + name + "."
	default:
		if err := d.storeSecret(ctx, a, name, ix.Message, p.SecretValue); err != nil {
			return err
		}
		msg = "Stored as $" + name + "; new sessions have it in their environment."
	}
	d.reg.DropDecision(s.ID, rid)
	if cur, found := d.reg.Get(s.ID); found {
		if err := d.deliver(ctx, cur, msg); err != nil {
			d.log.Warn("pagent: tell agent about secret", "session", s.ID, "err", err)
		}
	}
	return nil
}
