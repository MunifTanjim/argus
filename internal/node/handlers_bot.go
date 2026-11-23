package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/atomicfile"
	"github.com/MunifTanjim/argus/internal/bot"
	"github.com/MunifTanjim/argus/internal/botgit"
	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/session"
)

// SetBotStore enables bot.*; nil disables it. Call before Run.
func (d *Node) SetBotStore(s *bot.Store, cfg config.BotsConfig) {
	d.bots = s
	d.botCfg = cfg
	d.botPrimaryFile = config.GetStatePath("bot-primary")
	d.lastClientFile = config.GetStatePath("bot-last-client")
	d.routineStartFn = d.routineStart
}

func botErr(format string, args ...any) error {
	return &api.RPCError{Code: api.CodeInvalidRequest, Message: fmt.Sprintf(format, args...)}
}

// validBotName reports whether name is safe to use as a directory name.
func validBotName(name string) bool {
	if name == "" || len(name) > 64 || name[0] == '.' || strings.Contains(name, "..") {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}

func (d *Node) botStore() (*bot.Store, error) {
	if d.bots == nil {
		return nil, botErr("bots disabled")
	}
	return d.bots, nil
}

func (d *Node) botGitMode() string {
	if m := d.botCfg.Defaults.Git.Mode; m != "" {
		return m
	}
	return botgit.ModeNested
}

// primaryID returns the id of the primary bot: the node state file when it
// exists, otherwise the configured name.
func (d *Node) primaryID(ctx context.Context, s *bot.Store) string {
	if data, err := os.ReadFile(d.botPrimaryFile); err == nil {
		return strings.TrimSpace(string(data))
	}
	if name := d.botCfg.Primary; name != "" {
		if a, ok, _ := s.BotByName(ctx, name); ok {
			return a.ID
		}
	}
	return ""
}

func (d *Node) botInfo(ctx context.Context, s *bot.Store, a bot.Bot, primary string) api.BotInfo {
	info := api.BotInfo{
		ID: a.ID, Name: a.Name, Dir: a.Dir,
		Archived: !a.ArchivedAt.IsZero(),
		Primary:  primary != "" && a.ID == primary && a.ArchivedAt.IsZero(),
		Status:   d.botStatus(ctx, s, a.ID),
		NodeID:   d.id,
	}
	def, _ := bot.LoadDefinition(a.Dir)
	set := bot.Resolve(def, d.botCfg.Defaults)
	info.Agent, info.Model = set.Agent, set.Model
	if def != nil {
		info.Description = def.Description
	}
	info.Settings = botSettings(def, set)
	return info
}

// botLiveSessions returns the live sessions of the bot's threads.
func (d *Node) botLiveSessions(ctx context.Context, s *bot.Store, botID string) []session.Session {
	var out []session.Session
	for _, ls := range d.botLive(ctx, s, botID) {
		out = append(out, ls.Session)
	}
	return out
}

func (d *Node) botStatus(ctx context.Context, s *bot.Store, botID string) string {
	rank, status := 0, "idle"
	for _, sess := range d.botLiveSessions(ctx, s, botID) {
		r, name := 0, ""
		switch {
		case sess.Blocked():
			r, name = 4, "needs_you"
		case sess.Status == session.StatusAwaitingInput:
			r, name = 3, "waiting"
		case sess.Status == session.StatusWorking:
			r, name = 2, "working"
		}
		if r > rank {
			rank, status = r, name
		}
	}
	return status
}

func (d *Node) botFind(ctx context.Context, s *bot.Store, id string) (bot.Bot, error) {
	if id == "" {
		return bot.Bot{}, botErr("bot_id is required")
	}
	a, ok, err := s.BotByID(ctx, id)
	if err != nil {
		return a, botErr("%v", err)
	}
	if !ok {
		return a, botErr("bot not found")
	}
	return a, nil
}

func (d *Node) infoByID(ctx context.Context, s *bot.Store, id string) (api.BotInfo, error) {
	a, err := d.botFind(ctx, s, id)
	if err != nil {
		return api.BotInfo{}, err
	}
	return d.botInfo(ctx, s, a, d.primaryID(ctx, s)), nil
}

func (d *Node) adoptBotDir(ctx context.Context, dir string) {
	if d.projreg != nil {
		_, _ = d.projreg.AdoptWorkspace(ctx, dir, "")
	}
}

// botTarget resolves the node a create/add names. remote is true when it is
// another node; a call that came from a peer is always handled locally.
func (d *Node) botTarget(ctx context.Context, ref string) (remote bool, node string, err error) {
	if ref == "" {
		return false, d.id, nil
	}
	if _, onPeer := peerNodeFrom(ctx); onPeer {
		return false, d.id, nil
	}
	node, err = d.resolveNode(ref)
	if err != nil {
		return false, "", botErr("%v", err)
	}
	return node != d.id, node, nil
}

func (d *Node) handleBotCreate(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.BotCreateParams](params)
	if err != nil {
		return nil, err
	}
	if remote, node, err := d.botTarget(ctx, p.NodeID); err != nil {
		return nil, err
	} else if remote {
		p.NodeID = ""
		var out api.BotInfo
		return out, d.forward(ctx, node, api.MethodBotCreate, p, &out)
	}
	s, err := d.botStore()
	if err != nil {
		return nil, err
	}
	dir := p.Dir
	name := strings.TrimSpace(p.Name)
	if dir != "" {
		if dir, err = filepath.Abs(dir); err != nil {
			return nil, botErr("%v", err)
		}
	}
	if name == "" && dir != "" {
		name = filepath.Base(dir)
	}
	if !validBotName(name) {
		return nil, botErr("invalid bot name")
	}
	if dir == "" {
		dir = filepath.Join(d.botCfg.DefaultDir, name)
	}
	if err := d.checkBotFree(ctx, s, name, dir); err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(dir, bot.DefinitionFile)); err == nil {
		return nil, botErr("%s already has a bot definition; use add", dir)
	}

	def := &bot.Definition{}
	var from string
	if p.From != "" {
		src, ok, err := s.BotByName(ctx, p.From)
		if err == nil && !ok {
			src, ok, err = s.BotByID(ctx, p.From)
		}
		if err != nil {
			return nil, botErr("%v", err)
		}
		if !ok {
			return nil, botErr("bot %q not found", p.From)
		}
		if def, err = bot.LoadDefinition(src.Dir); err != nil {
			return nil, botErr("%v", err)
		}
		from = src.Dir
	}
	def.ID, def.Name = bot.NewID(), name
	if p.Agent != "" {
		def.Agent = p.Agent
	}
	if p.Model != "" {
		def.Model = p.Model
	}
	if p.Description != "" {
		def.Description = p.Description
	}
	if p.Instructions != "" {
		def.Instructions = p.Instructions
	}
	if from != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, botErr("%v", err)
		}
		if err := copyTree(filepath.Join(from, "skills"), filepath.Join(dir, "skills"), nil); err != nil {
			return nil, botErr("copy skills: %v", err)
		}
		if err := copyTree(filepath.Join(from, "routines"), filepath.Join(dir, "routines"), pauseRoutine); err != nil {
			return nil, botErr("copy routines: %v", err)
		}
	}
	if err := d.registerBot(ctx, s, dir, def); err != nil {
		return nil, err
	}
	d.notifyConns(api.MethodBotChanged)
	return d.infoByID(ctx, s, def.ID)
}

// registerBot scaffolds dir and records the bot, which must have a name
// and an id.
func (d *Node) registerBot(ctx context.Context, s *bot.Store, dir string, def *bot.Definition) error {
	if _, err := bot.Scaffold(ctx, dir, def, d.botGitMode(), d.botCfg.Defaults.Git.CommitFiles); err != nil {
		return botErr("scaffold: %v", err)
	}
	if err := s.InsertBot(ctx, bot.Bot{ID: def.ID, Name: def.Name, Dir: dir}); err != nil {
		return botErr("%v", err)
	}
	d.adoptBotDir(ctx, dir)
	return nil
}

func (d *Node) checkBotFree(ctx context.Context, s *bot.Store, name, dir string) error {
	if _, ok, err := s.BotByName(ctx, name); err != nil {
		return botErr("%v", err)
	} else if ok {
		return botErr("a bot named %q already exists", name)
	}
	if _, ok, err := s.BotByDir(ctx, dir); err != nil {
		return botErr("%v", err)
	} else if ok {
		return botErr("%s is already a bot directory", dir)
	}
	return nil
}

func (d *Node) handleBotAdd(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.BotAddParams](params)
	if err != nil {
		return nil, err
	}
	if remote, node, err := d.botTarget(ctx, p.NodeID); err != nil {
		return nil, err
	} else if remote {
		p.NodeID = ""
		var out api.BotAddResult
		return out, d.forward(ctx, node, api.MethodBotAdd, p, &out)
	}
	s, err := d.botStore()
	if err != nil {
		return nil, err
	}
	if p.Dir == "" {
		return nil, botErr("dir is required")
	}
	dir, err := filepath.Abs(p.Dir)
	if err != nil {
		return nil, botErr("%v", err)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return nil, botErr("%s is not a directory", dir)
	}

	def, err := bot.LoadDefinition(dir)
	if errors.Is(err, os.ErrNotExist) {
		def, err = &bot.Definition{}, nil
	}
	if err != nil {
		return nil, botErr("%v", err)
	}
	if def.Instructions == "" {
		text, found, err := bot.ImportInstructionFile(dir)
		if err != nil {
			return nil, botErr("%v", err)
		}
		if found {
			if !p.ImportInstructions {
				return api.BotAddResult{FoundInstructions: true}, nil
			}
			def.Instructions = text
		}
	}
	if n := strings.TrimSpace(p.Name); n != "" {
		def.Name = n
	}
	if def.Name == "" {
		def.Name = filepath.Base(dir)
	}
	if !validBotName(def.Name) {
		return nil, botErr("invalid bot name")
	}
	if def.ID == "" {
		def.ID = bot.NewID()
	}

	existing, found, err := s.BotByID(ctx, def.ID)
	if err != nil {
		return nil, botErr("%v", err)
	}
	if found {
		if existing.Dir != dir {
			return nil, botErr("bot %q is registered at %s", existing.Name, existing.Dir)
		}
		if existing.ArchivedAt.IsZero() {
			return nil, botErr("bot %q is already registered", existing.Name)
		}
	}
	if byName, ok, err := s.BotByName(ctx, def.Name); err != nil {
		return nil, botErr("%v", err)
	} else if ok && byName.ID != def.ID {
		return nil, botErr("a bot named %q already exists; choose another name", def.Name)
	}
	if !found {
		if _, ok, err := s.BotByDir(ctx, dir); err != nil {
			return nil, botErr("%v", err)
		} else if ok {
			return nil, botErr("%s is already a bot directory", dir)
		}
	}

	if err := def.Save(dir); err != nil {
		return nil, botErr("%v", err)
	}
	if found {
		if _, err := bot.Scaffold(ctx, dir, def, d.botGitMode(), d.botCfg.Defaults.Git.CommitFiles); err != nil {
			return nil, botErr("scaffold: %v", err)
		}
		if err := s.RestoreBot(ctx, def.ID); err != nil {
			return nil, botErr("%v", err)
		}
		if existing.Name != def.Name {
			if err := s.RenameBot(ctx, def.ID, def.Name); err != nil {
				return nil, botErr("%v", err)
			}
		}
		d.adoptBotDir(ctx, dir)
	} else if err := d.registerBot(ctx, s, dir, def); err != nil {
		return nil, err
	}
	d.notifyConns(api.MethodBotChanged)
	info, err := d.infoByID(ctx, s, def.ID)
	if err != nil {
		return nil, err
	}
	return api.BotAddResult{Bot: info}, nil
}

func (d *Node) handleBotList(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.BotListParams](params)
	if err != nil {
		return nil, err
	}
	s, err := d.botStore()
	if err != nil {
		return nil, err
	}
	bots, err := s.ListBots(ctx, p.IncludeArchived)
	if err != nil {
		return nil, botErr("%v", err)
	}
	primary := d.primaryID(ctx, s)
	res := api.BotListResult{Bots: make([]api.BotInfo, len(bots))}
	for i, a := range bots {
		res.Bots[i] = d.botInfo(ctx, s, a, primary)
		if res.Bots[i].Primary {
			res.Primary = a.ID
		}
	}
	if res.Primary != "" {
		if fi, err := os.Stat(d.botPrimaryFile); err == nil {
			res.PrimarySince = fi.ModTime().UTC()
		}
	}
	return res, nil
}

func (d *Node) handleBotShow(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.BotRef](params)
	if err != nil {
		return nil, err
	}
	s, err := d.botStore()
	if err != nil {
		return nil, err
	}
	return d.infoByID(ctx, s, p.BotID)
}

func (d *Node) handleBotRename(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.BotRenameParams](params)
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
	name := strings.TrimSpace(p.Name)
	if !validBotName(name) {
		return nil, botErr("invalid bot name")
	}
	if name != a.Name {
		if _, ok, err := s.BotByName(ctx, name); err != nil {
			return nil, botErr("%v", err)
		} else if ok {
			return nil, botErr("a bot named %q already exists", name)
		}
		def, err := bot.LoadDefinition(a.Dir)
		if err != nil {
			return nil, botErr("%v", err)
		}
		def.Name = name
		if err := def.Save(a.Dir); err != nil {
			return nil, botErr("%v", err)
		}
		repo, err := botgit.Open(ctx, a.Dir, d.botGitMode())
		if err != nil {
			return nil, botErr("%v", err)
		}
		if _, err := repo.CommitPaths(ctx, "bot: rename to "+name, []botgit.Trailer{
			{Key: "Argus-Bot", Value: name},
			{Key: "Argus-Bot-Id", Value: a.ID},
			{Key: "Argus-Source", Value: "you"},
		}, []string{bot.DefinitionFile}, true); err != nil {
			return nil, botErr("%v", err)
		}
		if err := s.RenameBot(ctx, a.ID, name); err != nil {
			return nil, botErr("%v", err)
		}
		d.notifyConns(api.MethodBotChanged)
	}
	return d.infoByID(ctx, s, a.ID)
}

func (d *Node) handleBotForget(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.BotForgetParams](params)
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
	if p.Purge {
		if len(d.botLiveSessions(ctx, s, a.ID)) > 0 {
			return nil, botErr("bot %q has a live session; end it first", a.Name)
		}
		err = s.PurgeBot(ctx, a.ID)
		if err == nil {
			_ = os.Remove(d.tidyIdleFile(a.ID))
		}
	} else {
		err = s.ArchiveBot(ctx, a.ID)
	}
	if err != nil {
		return nil, botErr("%v", err)
	}
	if d.primaryID(ctx, s) == a.ID {
		if err := d.writePrimary(""); err != nil {
			return nil, botErr("%v", err)
		}
	}
	d.notifyConns(api.MethodBotChanged)
	return struct{}{}, nil
}

func (d *Node) handleBotSetPrimary(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.BotPrimaryParams](params)
	if err != nil {
		return nil, err
	}
	s, err := d.botStore()
	if err != nil {
		return nil, err
	}
	if p.BotID != "" {
		a, err := d.botFind(ctx, s, p.BotID)
		if err != nil {
			return nil, err
		}
		if !a.ArchivedAt.IsZero() {
			return nil, botErr("bot %q is archived", a.Name)
		}
	}
	if err := d.writePrimary(p.BotID); err != nil {
		return nil, botErr("%v", err)
	}
	d.notifyConns(api.MethodBotChanged)
	return struct{}{}, nil
}

func (d *Node) writePrimary(id string) error {
	if err := os.MkdirAll(filepath.Dir(d.botPrimaryFile), 0o755); err != nil {
		return err
	}
	return atomicfile.Write(d.botPrimaryFile, []byte(id))
}

// copyTree copies src into dst. transform, when set, rewrites each file's
// content. A missing src is not an error.
func copyTree(src, dst string, transform func(name string, data []byte) ([]byte, error)) error {
	if _, err := os.Stat(src); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return filepath.WalkDir(src, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if e.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !e.Type().IsRegular() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if transform != nil {
			if data, err = transform(rel, data); err != nil {
				return err
			}
		}
		return os.WriteFile(target, data, 0o644)
	})
}

// pauseRoutine sets enabled: false in a routine file.
func pauseRoutine(name string, data []byte) ([]byte, error) {
	ext := strings.ToLower(filepath.Ext(name))
	if ext != ".yaml" && ext != ".yml" {
		return data, nil
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return data, nil
	}
	m := doc.Content[0]
	set := false
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == "enabled" {
			m.Content[i+1] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "false"}
			set = true
		}
	}
	if !set {
		m.Content = append(m.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "enabled"},
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "false"})
	}
	return yaml.Marshal(&doc)
}
