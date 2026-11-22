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

	"github.com/MunifTanjim/argus/internal/agentgit"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/pagent"
	"github.com/MunifTanjim/argus/internal/session"
)

// SetAgentStore enables pagent.*; nil disables it. Call before Run.
func (d *Node) SetAgentStore(s *pagent.Store, cfg config.AgentsConfig) {
	d.pagents = s
	d.pagentCfg = cfg
	d.pagentPrimaryFile = config.GetStatePath("pagent-primary")
}

func pagentErr(format string, args ...any) error {
	return &api.RPCError{Code: api.CodeInvalidRequest, Message: fmt.Sprintf(format, args...)}
}

func (d *Node) pagentStore() (*pagent.Store, error) {
	if d.pagents == nil {
		return nil, pagentErr("agents disabled")
	}
	return d.pagents, nil
}

func (d *Node) pagentGitMode() string {
	if m := d.pagentCfg.Defaults.Git.Mode; m != "" {
		return m
	}
	return agentgit.ModeNested
}

// primaryID returns the id of the primary agent: the node state file when it
// exists, otherwise the configured name.
func (d *Node) primaryID(ctx context.Context, s *pagent.Store) string {
	if data, err := os.ReadFile(d.pagentPrimaryFile); err == nil {
		return strings.TrimSpace(string(data))
	}
	if name := d.pagentCfg.Primary; name != "" {
		if a, ok, _ := s.AgentByName(ctx, name); ok {
			return a.ID
		}
	}
	return ""
}

func (d *Node) pagentInfo(ctx context.Context, s *pagent.Store, a pagent.Agent, primary string) api.PAgentInfo {
	info := api.PAgentInfo{
		ID: a.ID, Name: a.Name, Dir: a.Dir,
		Archived: !a.ArchivedAt.IsZero(),
		Primary:  primary != "" && a.ID == primary && a.ArchivedAt.IsZero(),
		Status:   d.pagentStatus(ctx, s, a.ID),
		NodeID:   d.id,
	}
	def, _ := pagent.LoadDefinition(a.Dir)
	set := pagent.Resolve(def, d.pagentCfg.Defaults)
	info.Agent, info.Model = set.Agent, set.Model
	if def != nil {
		info.Description = def.Description
	}
	return info
}

// liveSessions returns the registry sessions of the agent's threads that have
// not ended.
func (d *Node) pagentLiveSessions(ctx context.Context, s *pagent.Store, agentID string) []session.Session {
	threads, err := s.ListThreads(ctx, agentID)
	if err != nil {
		return nil
	}
	var out []session.Session
	for _, th := range threads {
		links, err := s.ThreadSessions(ctx, th.ID)
		if err != nil {
			continue
		}
		for _, l := range links {
			if !l.EndedAt.IsZero() {
				continue
			}
			if sess, ok := d.reg.Get(l.SessionID); ok {
				out = append(out, sess)
			}
		}
	}
	return out
}

func (d *Node) pagentStatus(ctx context.Context, s *pagent.Store, agentID string) string {
	rank, status := 0, "idle"
	for _, sess := range d.pagentLiveSessions(ctx, s, agentID) {
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

func (d *Node) pagentFind(ctx context.Context, s *pagent.Store, id string) (pagent.Agent, error) {
	if id == "" {
		return pagent.Agent{}, pagentErr("agent_id is required")
	}
	a, ok, err := s.AgentByID(ctx, id)
	if err != nil {
		return a, pagentErr("%v", err)
	}
	if !ok {
		return a, pagentErr("agent not found")
	}
	return a, nil
}

func (d *Node) infoByID(ctx context.Context, s *pagent.Store, id string) (api.PAgentInfo, error) {
	a, err := d.pagentFind(ctx, s, id)
	if err != nil {
		return api.PAgentInfo{}, err
	}
	return d.pagentInfo(ctx, s, a, d.primaryID(ctx, s)), nil
}

func (d *Node) adoptAgentDir(ctx context.Context, dir string) {
	if d.projreg != nil {
		_, _ = d.projreg.AdoptWorkspace(ctx, dir, "")
	}
}

func (d *Node) handlePAgentCreate(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.PAgentCreateParams](params)
	if err != nil {
		return nil, err
	}
	s, err := d.pagentStore()
	if err != nil {
		return nil, err
	}
	dir := p.Dir
	name := strings.TrimSpace(p.Name)
	if dir != "" {
		if dir, err = filepath.Abs(dir); err != nil {
			return nil, pagentErr("%v", err)
		}
	}
	if name == "" && dir != "" {
		name = filepath.Base(dir)
	}
	if name == "" {
		return nil, pagentErr("name is required")
	}
	if dir == "" {
		dir = filepath.Join(d.pagentCfg.DefaultDir, name)
	}
	if err := d.checkAgentFree(ctx, s, name, dir); err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(dir, pagent.DefinitionFile)); err == nil {
		return nil, pagentErr("%s already has an agent definition; use add", dir)
	}

	def := &pagent.Definition{}
	var from string
	if p.From != "" {
		src, ok, err := s.AgentByName(ctx, p.From)
		if err == nil && !ok {
			src, ok, err = s.AgentByID(ctx, p.From)
		}
		if err != nil {
			return nil, pagentErr("%v", err)
		}
		if !ok {
			return nil, pagentErr("agent %q not found", p.From)
		}
		if def, err = pagent.LoadDefinition(src.Dir); err != nil {
			return nil, pagentErr("%v", err)
		}
		from = src.Dir
	}
	def.ID, def.Name = pagent.NewID(), name
	if p.Agent != "" {
		def.Agent = p.Agent
	}
	if p.Model != "" {
		def.Model = p.Model
	}
	if from != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, pagentErr("%v", err)
		}
		if err := copyTree(filepath.Join(from, "skills"), filepath.Join(dir, "skills"), nil); err != nil {
			return nil, pagentErr("copy skills: %v", err)
		}
		if err := copyTree(filepath.Join(from, "routines"), filepath.Join(dir, "routines"), pauseRoutine); err != nil {
			return nil, pagentErr("copy routines: %v", err)
		}
	}
	if err := d.registerAgent(ctx, s, dir, def); err != nil {
		return nil, err
	}
	d.notifyConns(api.MethodPAgentChanged)
	return d.infoByID(ctx, s, def.ID)
}

// registerAgent scaffolds dir and records the agent, which must have a name
// and an id.
func (d *Node) registerAgent(ctx context.Context, s *pagent.Store, dir string, def *pagent.Definition) error {
	if _, err := pagent.Scaffold(ctx, dir, def, d.pagentGitMode(), d.pagentCfg.Defaults.Git.CommitFiles); err != nil {
		return pagentErr("scaffold: %v", err)
	}
	if err := s.InsertAgent(ctx, pagent.Agent{ID: def.ID, Name: def.Name, Dir: dir}); err != nil {
		return pagentErr("%v", err)
	}
	d.adoptAgentDir(ctx, dir)
	return nil
}

func (d *Node) checkAgentFree(ctx context.Context, s *pagent.Store, name, dir string) error {
	if _, ok, err := s.AgentByName(ctx, name); err != nil {
		return pagentErr("%v", err)
	} else if ok {
		return pagentErr("an agent named %q already exists", name)
	}
	if _, ok, err := s.AgentByDir(ctx, dir); err != nil {
		return pagentErr("%v", err)
	} else if ok {
		return pagentErr("%s is already an agent directory", dir)
	}
	return nil
}

func (d *Node) handlePAgentAdd(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.PAgentAddParams](params)
	if err != nil {
		return nil, err
	}
	s, err := d.pagentStore()
	if err != nil {
		return nil, err
	}
	if p.Dir == "" {
		return nil, pagentErr("dir is required")
	}
	dir, err := filepath.Abs(p.Dir)
	if err != nil {
		return nil, pagentErr("%v", err)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return nil, pagentErr("%s is not a directory", dir)
	}

	def, err := pagent.LoadDefinition(dir)
	if errors.Is(err, os.ErrNotExist) {
		def, err = &pagent.Definition{}, nil
	}
	if err != nil {
		return nil, pagentErr("%v", err)
	}
	if def.Instructions == "" {
		text, found, err := pagent.ImportInstructionFile(dir)
		if err != nil {
			return nil, pagentErr("%v", err)
		}
		if found {
			if !p.ImportInstructions {
				return api.PAgentAddResult{FoundInstructions: true}, nil
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
	if def.ID == "" {
		def.ID = pagent.NewID()
	}

	existing, found, err := s.AgentByID(ctx, def.ID)
	if err != nil {
		return nil, pagentErr("%v", err)
	}
	if found {
		if existing.Dir != dir {
			return nil, pagentErr("agent %q is registered at %s", existing.Name, existing.Dir)
		}
		if existing.ArchivedAt.IsZero() {
			return nil, pagentErr("agent %q is already registered", existing.Name)
		}
	}
	if byName, ok, err := s.AgentByName(ctx, def.Name); err != nil {
		return nil, pagentErr("%v", err)
	} else if ok && byName.ID != def.ID {
		return nil, pagentErr("an agent named %q already exists; choose another name", def.Name)
	}
	if !found {
		if _, ok, err := s.AgentByDir(ctx, dir); err != nil {
			return nil, pagentErr("%v", err)
		} else if ok {
			return nil, pagentErr("%s is already an agent directory", dir)
		}
	}

	if err := def.Save(dir); err != nil {
		return nil, pagentErr("%v", err)
	}
	if found {
		if _, err := pagent.Scaffold(ctx, dir, def, d.pagentGitMode(), d.pagentCfg.Defaults.Git.CommitFiles); err != nil {
			return nil, pagentErr("scaffold: %v", err)
		}
		if err := s.RestoreAgent(ctx, def.ID); err != nil {
			return nil, pagentErr("%v", err)
		}
		if existing.Name != def.Name {
			if err := s.RenameAgent(ctx, def.ID, def.Name); err != nil {
				return nil, pagentErr("%v", err)
			}
		}
		d.adoptAgentDir(ctx, dir)
	} else if err := d.registerAgent(ctx, s, dir, def); err != nil {
		return nil, err
	}
	d.notifyConns(api.MethodPAgentChanged)
	info, err := d.infoByID(ctx, s, def.ID)
	if err != nil {
		return nil, err
	}
	return api.PAgentAddResult{Agent: info}, nil
}

func (d *Node) handlePAgentList(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.PAgentListParams](params)
	if err != nil {
		return nil, err
	}
	s, err := d.pagentStore()
	if err != nil {
		return nil, err
	}
	agents, err := s.ListAgents(ctx, p.IncludeArchived)
	if err != nil {
		return nil, pagentErr("%v", err)
	}
	primary := d.primaryID(ctx, s)
	res := api.PAgentListResult{Agents: make([]api.PAgentInfo, len(agents))}
	for i, a := range agents {
		res.Agents[i] = d.pagentInfo(ctx, s, a, primary)
		if res.Agents[i].Primary {
			res.Primary = a.ID
		}
	}
	return res, nil
}

func (d *Node) handlePAgentShow(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.PAgentRef](params)
	if err != nil {
		return nil, err
	}
	s, err := d.pagentStore()
	if err != nil {
		return nil, err
	}
	return d.infoByID(ctx, s, p.AgentID)
}

func (d *Node) handlePAgentRename(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.PAgentRenameParams](params)
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
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return nil, pagentErr("name is required")
	}
	if name != a.Name {
		if _, ok, err := s.AgentByName(ctx, name); err != nil {
			return nil, pagentErr("%v", err)
		} else if ok {
			return nil, pagentErr("an agent named %q already exists", name)
		}
		def, err := pagent.LoadDefinition(a.Dir)
		if err != nil {
			return nil, pagentErr("%v", err)
		}
		def.Name = name
		if err := def.Save(a.Dir); err != nil {
			return nil, pagentErr("%v", err)
		}
		repo, err := agentgit.Open(ctx, a.Dir, d.pagentGitMode())
		if err != nil {
			return nil, pagentErr("%v", err)
		}
		if _, err := repo.CommitPaths(ctx, "agent: rename to "+name, []agentgit.Trailer{
			{Key: "Argus-Agent", Value: name},
			{Key: "Argus-Agent-Id", Value: a.ID},
			{Key: "Argus-Source", Value: "you"},
		}, []string{pagent.DefinitionFile}, true); err != nil {
			return nil, pagentErr("%v", err)
		}
		if err := s.RenameAgent(ctx, a.ID, name); err != nil {
			return nil, pagentErr("%v", err)
		}
		d.notifyConns(api.MethodPAgentChanged)
	}
	return d.infoByID(ctx, s, a.ID)
}

func (d *Node) handlePAgentForget(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.PAgentForgetParams](params)
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
	if p.Purge {
		if len(d.pagentLiveSessions(ctx, s, a.ID)) > 0 {
			return nil, pagentErr("agent %q has a live session; end it first", a.Name)
		}
		err = s.PurgeAgent(ctx, a.ID)
	} else {
		err = s.ArchiveAgent(ctx, a.ID)
	}
	if err != nil {
		return nil, pagentErr("%v", err)
	}
	d.notifyConns(api.MethodPAgentChanged)
	return struct{}{}, nil
}

func (d *Node) handlePAgentSetPrimary(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.PAgentPrimaryParams](params)
	if err != nil {
		return nil, err
	}
	s, err := d.pagentStore()
	if err != nil {
		return nil, err
	}
	if p.AgentID != "" {
		a, err := d.pagentFind(ctx, s, p.AgentID)
		if err != nil {
			return nil, err
		}
		if !a.ArchivedAt.IsZero() {
			return nil, pagentErr("agent %q is archived", a.Name)
		}
	}
	if err := os.MkdirAll(filepath.Dir(d.pagentPrimaryFile), 0o755); err != nil {
		return nil, pagentErr("%v", err)
	}
	if err := os.WriteFile(d.pagentPrimaryFile, []byte(p.AgentID), 0o644); err != nil {
		return nil, pagentErr("%v", err)
	}
	d.notifyConns(api.MethodPAgentChanged)
	return struct{}{}, nil
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
