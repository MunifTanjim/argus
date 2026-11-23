package node

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/bot"
	"github.com/MunifTanjim/argus/internal/bot/policy"
	"github.com/MunifTanjim/argus/internal/botgit"
)

// Before every Argus commit in a bot repository, the files that decide what
// git runs (hooks, config) and what bots may do unattended (the approvals in
// bot.yaml and approvals.yaml) must match what Argus last recorded. A change
// Argus did not make refuses the commit, before git runs, until the user
// trusts the bot again.
const (
	healthUntrusted = "untrusted-change"

	// trustProfileKey is the record shared by every bot: approvals.yaml and
	// the profile repository.
	trustProfileKey = "\x00profile"

	itemBotApprovals    = bot.DefinitionFile + " approvals"
	itemGlobalApprovals = globalApprovalsFile
	profileItemPrefix   = "profile/"
)

var errUntrusted = errors.New("protected files changed")

// handEditErr reports a failure to commit hand edits before a memory write:
// a refusal for untrusted changes can succeed once the user trusts the bot,
// so it is retryable.
func handEditErr(err error) error {
	if errors.Is(err, errUntrusted) {
		return memoryWriteErr(err)
	}
	return botErr("%v", err)
}

// openBotRepo opens a's repository with the trust guard on its commits.
func (d *Node) openBotRepo(ctx context.Context, a bot.Bot) (*botgit.Repo, error) {
	repo, err := botgit.Open(ctx, a.Dir, d.botGitMode())
	if err != nil {
		return nil, err
	}
	if repo.Created {
		// Argus made the hooks and config just now.
		if cur, err := repo.Protected(ctx); err == nil {
			if err := d.trustAccept(ctx, a.ID, cur, func(k string) bool { return k != itemBotApprovals }); err != nil {
				d.log.Warn("bot: record trust", "bot", a.ID, "err", err)
			}
		}
	}
	repo.Guard = func(ctx context.Context) error { return d.checkTrust(ctx, a, repo) }
	return repo, nil
}

// guardProfileRepo puts the trust guard on the profile repository; commits
// there on a's behalf check only the shared record.
func (d *Node) guardProfileRepo(ctx context.Context, a bot.Bot, repo *botgit.Repo) {
	if repo.Created {
		if cur, err := d.profileTrustItems(ctx); err == nil {
			if err := d.trustAccept(ctx, trustProfileKey, cur, func(k string) bool { return strings.HasPrefix(k, profileItemPrefix) }); err != nil {
				d.log.Warn("bot: record trust", "err", err)
			}
		}
	}
	repo.Guard = func(ctx context.Context) error { return d.checkTrust(ctx, a, nil) }
}

// checkTrust compares the bot's record (when repo is set) and the shared
// record with the files now. A record seen for the first time is stored. Any
// difference raises a health notice and refuses.
func (d *Node) checkTrust(ctx context.Context, a bot.Bot, repo *botgit.Repo) error {
	s, err := d.botStore()
	if err != nil {
		return err
	}
	var changed []string
	check := func(key string, cur map[string]string) error {
		old, ok, err := s.Trust(ctx, key)
		if err != nil {
			return err
		}
		if !ok {
			return s.SetTrust(ctx, key, cur)
		}
		changed = append(changed, botgit.ProtectedChanges(old, cur)...)
		return nil
	}
	if repo != nil {
		cur, err := botTrustItems(ctx, a, repo)
		if err != nil {
			return fmt.Errorf("check protected files: %w", err)
		}
		if err := check(a.ID, cur); err != nil {
			return err
		}
	}
	shared, err := d.profileTrustItems(ctx)
	if err != nil {
		return fmt.Errorf("check protected files: %w", err)
	}
	if err := check(trustProfileKey, shared); err != nil {
		return err
	}
	if len(changed) == 0 {
		d.botHealthResolved(a, healthUntrusted)
		return nil
	}
	list := strings.Join(changed, ", ")
	d.healthNotice(ctx, a, healthUntrusted, fmt.Sprintf(
		"commits paused: %s changed outside Argus; if you made this change, run `argus bot trust %s`", list, a.Name))
	return fmt.Errorf("%w since Argus last recorded them: %s; run `argus bot trust %s` to accept", errUntrusted, list, a.Name)
}

// botTrustItems fingerprints a's repository and the approvals in its
// bot.yaml.
func botTrustItems(ctx context.Context, a bot.Bot, repo *botgit.Repo) (map[string]string, error) {
	items, err := repo.Protected(ctx)
	if err != nil {
		return nil, err
	}
	if h, ok := approvalsHash(a.Dir); ok {
		items[itemBotApprovals] = h
	}
	return items, nil
}

// approvalsHash hashes the approval rules bot.yaml in dir sets, so edits to
// other keys or to formatting do not count. ok is false without a bot.yaml.
func approvalsHash(dir string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(dir, bot.DefinitionFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false
		}
		return "unreadable: " + err.Error(), true
	}
	var doc struct {
		Approvals []policy.Rule `yaml:"approvals"`
	}
	canon := data
	if yaml.Unmarshal(data, &doc) == nil {
		if canon, err = json.Marshal(doc.Approvals); err != nil {
			canon = data
		}
	}
	sum := sha256.Sum256(canon)
	return hex.EncodeToString(sum[:]), true
}

// profileTrustItems fingerprints approvals.yaml and, once it exists, the
// profile repository.
func (d *Node) profileTrustItems(ctx context.Context) (map[string]string, error) {
	items := map[string]string{}
	dir := d.botCfg.ProfileDir
	if dir == "" {
		return items, nil
	}
	if data, err := os.ReadFile(filepath.Join(dir, globalApprovalsFile)); err == nil {
		sum := sha256.Sum256(data)
		items[itemGlobalApprovals] = hex.EncodeToString(sum[:])
	} else if !errors.Is(err, os.ErrNotExist) {
		items[itemGlobalApprovals] = "unreadable: " + err.Error()
	}
	if _, err := os.Lstat(filepath.Join(dir, ".git")); err != nil {
		return items, nil
	}
	repo, err := botgit.Open(ctx, dir, botgit.ModeNested)
	if err != nil {
		return nil, err
	}
	git, err := repo.Protected(ctx)
	if err != nil {
		return nil, err
	}
	for k, v := range git {
		items[profileItemPrefix+k] = v
	}
	return items, nil
}

// trustAccept records the current hashes of the items accept selects, for a
// change Argus made itself; the rest of key's record stays. A key with no
// record is left for the first check to record.
func (d *Node) trustAccept(ctx context.Context, key string, cur map[string]string, accept func(string) bool) error {
	s, err := d.botStore()
	if err != nil {
		return err
	}
	old, ok, err := s.Trust(ctx, key)
	if err != nil || !ok {
		return err
	}
	for k := range old {
		if accept(k) {
			delete(old, k)
		}
	}
	for k, v := range cur {
		if accept(k) {
			old[k] = v
		}
	}
	return s.SetTrust(ctx, key, old)
}

// trustRegistered records a's protected files when the user creates or adds
// the bot.
func (d *Node) trustRegistered(ctx context.Context, a bot.Bot) {
	repo, err := botgit.Open(ctx, a.Dir, d.botGitMode())
	if err == nil {
		var items map[string]string
		if items, err = botTrustItems(ctx, a, repo); err == nil {
			err = d.bots.SetTrust(ctx, a.ID, items)
		}
	}
	if err != nil {
		d.log.Warn("bot: record trust", "bot", a.ID, "err", err)
	}
}

// acceptApprovalsEdit records the approvals in a's bot.yaml after Argus
// changed them.
func (d *Node) acceptApprovalsEdit(ctx context.Context, a bot.Bot) error {
	cur := map[string]string{}
	if h, ok := approvalsHash(a.Dir); ok {
		cur[itemBotApprovals] = h
	}
	return d.trustAccept(ctx, a.ID, cur, func(k string) bool { return k == itemBotApprovals })
}

// handleBotTrust records the protected files of a bot and the shared ones as
// they are now, accepting changes made outside Argus.
func (d *Node) handleBotTrust(ctx context.Context, params json.RawMessage) (any, error) {
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
	defer lockMemory(a.ID)()
	repo, err := botgit.Open(ctx, a.Dir, d.botGitMode())
	if err != nil {
		return nil, botErr("%v", err)
	}
	own, err := botTrustItems(ctx, a, repo)
	if err != nil {
		return nil, botErr("%v", err)
	}
	shared, err := d.profileTrustItems(ctx)
	if err != nil {
		return nil, botErr("%v", err)
	}
	res := api.BotTrustResult{Accepted: []string{}}
	for _, r := range []struct {
		key   string
		items map[string]string
	}{{a.ID, own}, {trustProfileKey, shared}} {
		if old, ok, err := s.Trust(ctx, r.key); err != nil {
			return nil, botErr("%v", err)
		} else if ok {
			res.Accepted = append(res.Accepted, botgit.ProtectedChanges(old, r.items)...)
		}
		if err := s.SetTrust(ctx, r.key, r.items); err != nil {
			return nil, botErr("%v", err)
		}
	}
	d.botHealthResolved(a, healthUntrusted)
	d.notifyConns(api.MethodBotChanged)
	return res, nil
}
