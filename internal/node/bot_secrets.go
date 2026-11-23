package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/bot"
	"github.com/MunifTanjim/argus/internal/bot/policy"
	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/session"
)

const (
	secretGetTool       = "secret get"
	decidedSecretStore  = "secret store"
	secretKeyFile       = "bot-secrets.key"
	secretRequestPrefix = "secret:"
	secretSave          = "save"
	secretDecline       = "decline"
)

// secretState holds the secret store's key, opened on first use.
type secretState struct {
	mu   sync.Mutex
	path string // key file; empty means the data directory's
	box  *bot.SecretBox
}

func (d *Node) secretBox() (*bot.SecretBox, error) {
	d.secrets.mu.Lock()
	defer d.secrets.mu.Unlock()
	if d.secrets.box != nil {
		return d.secrets.box, nil
	}
	path := d.secrets.path
	if path == "" {
		path = config.GetDataPath(secretKeyFile)
	}
	b, err := bot.OpenSecretBox(path)
	if err != nil {
		return nil, err
	}
	d.secrets.box = b
	return b, nil
}

// botSecrets returns the bot's secrets with their values.
func (d *Node) botSecrets(ctx context.Context, botID string) []bot.Secret {
	if d.bots == nil || botID == "" {
		return nil
	}
	list, err := d.bots.Secrets(ctx, botID)
	if err != nil || len(list) == 0 {
		return nil
	}
	b, err := d.secretBox()
	if err != nil {
		d.log.Warn("bot: secret store", "err", err)
		return nil
	}
	vals, err := d.bots.SecretValues(ctx, b, botID)
	if err != nil {
		d.log.Warn("bot: read secrets", "bot", botID, "err", err)
	}
	return vals
}

// secretList returns the bot's secret names and descriptions.
func (d *Node) secretList(ctx context.Context, botID string) []bot.Secret {
	if d.bots == nil {
		return nil
	}
	list, err := d.bots.Secrets(ctx, botID)
	if err != nil {
		d.log.Warn("bot: list secrets", "bot", botID, "err", err)
		return nil
	}
	return list
}

// redact replaces the bot's known secret values in text with «NAME».
func (d *Node) redact(ctx context.Context, botID, text string) string {
	if text == "" {
		return text
	}
	return bot.Redact(text, d.botSecrets(ctx, botID))
}

// redactJSON redacts a JSON document, keeping it valid JSON.
func (d *Node) redactJSON(ctx context.Context, botID string, raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	out := json.RawMessage(d.redact(ctx, botID, string(raw)))
	if json.Valid(out) {
		return out
	}
	q, _ := json.Marshal(string(out))
	return q
}

func (d *Node) handleBotSecretSet(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.BotSecretSetParams](params)
	if err != nil {
		return nil, err
	}
	s, err := d.botStore()
	if err != nil {
		return nil, err
	}
	a, err := d.botFind(ctx, s, p.BotID)
	if err != nil {
		return nil, err
	}
	if err := d.storeSecret(ctx, a, p.Name, p.Description, p.Value); err != nil {
		return nil, err
	}
	return struct{}{}, nil
}

// reservedEnv are environment variables a secret can't be named: the shell,
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
	if !bot.ValidSecretName(name) {
		return botErr("secret name %q must be like GH_TOKEN: capital letters, digits and _", name)
	}
	if secretNameReserved(name) {
		return botErr("secret name %q is a reserved environment variable", name)
	}
	return nil
}

func (d *Node) storeSecret(ctx context.Context, a bot.Bot, name, description, value string) error {
	if err := checkSecretName(name); err != nil {
		return err
	}
	if value == "" {
		return botErr("secret value is empty")
	}
	b, err := d.secretBox()
	if err != nil {
		return botErr("%v", err)
	}
	enc, err := b.Seal(a.ID, name, value)
	if err != nil {
		return botErr("%v", err)
	}
	if err := d.bots.SetSecret(ctx, a.ID, name, strings.TrimSpace(description), enc); err != nil {
		return botErr("%v", err)
	}
	d.notifyConns(api.MethodBotChanged)
	return nil
}

func (d *Node) handleBotSecretList(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.BotRef](params)
	if err != nil {
		return nil, err
	}
	s, err := d.botStore()
	if err != nil {
		return nil, err
	}
	a, err := d.botFind(ctx, s, p.BotID)
	if err != nil {
		return nil, err
	}
	list, err := s.Secrets(ctx, a.ID)
	if err != nil {
		return nil, botErr("%v", err)
	}
	res := api.BotSecretListResult{Secrets: make([]api.BotSecretInfo, len(list))}
	for i, sec := range list {
		res.Secrets[i] = api.BotSecretInfo{Name: sec.Name, Description: sec.Description, UpdatedAt: sec.UpdatedAt}
	}
	return res, nil
}

func (d *Node) handleBotSecretRm(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.BotSecretRef](params)
	if err != nil {
		return nil, err
	}
	s, err := d.botStore()
	if err != nil {
		return nil, err
	}
	a, err := d.botFind(ctx, s, p.BotID)
	if err != nil {
		return nil, err
	}
	if err := s.DeleteSecret(ctx, a.ID, p.Name); errors.Is(err, bot.ErrNotFound) {
		return nil, botErr("secret %s not found", p.Name)
	} else if err != nil {
		return nil, botErr("%v", err)
	}
	d.notifyConns(api.MethodBotChanged)
	return struct{}{}, nil
}

type requestSecretParams struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func isSecretRequest(requestID string) bool { return strings.HasPrefix(requestID, secretRequestPrefix) }

// mcpRequestSecret asks the user for a secret value. The value goes to the
// node's secret store; the bot only hears its name.
func (d *Node) mcpRequestSecret(ctx context.Context, caller callerRef, p requestSecretParams) (any, error) {
	a, th, err := d.botCaller(ctx, caller)
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
	d.botNotify(ctx, a, th.ID, NotifyNeedsYou, th.StartedBy, a.Name+" asks for a secret", name)
	return map[string]string{"status": "waiting for the user; you'll get a message when it is stored"}, nil
}

// respondSecret stores the value of a secret interaction and tells the bot
// the name only.
func (d *Node) respondSecret(ctx context.Context, s session.Session, rid string, p api.RespondParams) error {
	ix, ok := d.reg.Decision(s.ID, rid)
	if !ok {
		return errRequestGone
	}
	tagged := s
	tagged.BotID, tagged.BotThreadID = d.botTag(s)
	a, _, err := d.botCaller(ctx, callerRef{Local: &tagged})
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
		msg = "Stored as " + name + "; read it with `argus secret get " + name + "`."
	}
	d.reg.DropDecision(s.ID, rid)
	if cur, found := d.reg.Get(s.ID); found {
		if err := d.deliver(ctx, cur, msg); err != nil {
			d.log.Warn("bot: tell bot about secret", "session", s.ID, "err", err)
		}
	}
	return nil
}

// errNotBotSession is what secret get answers a caller that is not a bot's
// session.
var errNotBotSession = errors.New("not a bot session")

// handleBotSecretGet returns one secret of the bot whose session ran the
// caller. Only the local socket serves it: the caller is found by its process.
func (d *Node) handleBotSecretGet(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.BotSecretGetParams](params)
	if err != nil {
		return nil, err
	}
	caller, err := d.callerFrom(ctx, p.Caller)
	if err != nil {
		return nil, err
	}
	if caller.Local == nil {
		return nil, errNotBotSession
	}
	tagged := *caller.Local
	tagged.BotID, tagged.BotThreadID = d.botTag(tagged)
	a, th, err := d.botCaller(ctx, callerRef{Local: &tagged})
	if err != nil {
		return nil, errNotBotSession
	}
	sc := approvalScope{bot: a, thread: th, sessionID: tagged.ID, cli: tagged.Agent}
	action := policy.Action{Tool: secretGetTool, Target: p.Name}
	for _, sec := range d.botSecrets(ctx, a.ID) {
		if sec.Name == p.Name {
			d.recordActivity(ctx, sc, action, policy.OutcomeAllow, decidedSecretStore, "", "", "")
			return api.BotSecretGetResult{Value: sec.Value}, nil
		}
	}
	reason := fmt.Sprintf("no secret %s for this bot", p.Name)
	d.recordActivity(ctx, sc, action, policy.OutcomeDeny, decidedSecretStore, "", reason, "")
	return nil, errors.New(reason)
}
