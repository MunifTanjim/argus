package node

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MunifTanjim/argus/internal/agentgit"
	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/pagent"
	"github.com/MunifTanjim/argus/internal/pagent/memory"
)

const (
	memoryDir        = "memory"
	memoryFile       = "MEMORY.md"
	userFile         = "USER.md"
	profileFile      = "PROFILE.md"
	archiveFile      = "topics/archive.md"
	memoryTitleRunes = 50

	memoryModeOff    = "off"
	memoryModeReview = "review"

	evMemoryChanged  = "memory.changed"
	evMemoryProposed = "memory.proposed"
	evMemoryBlocked  = "memory.blocked"
	evMemoryError    = "memory.error"
)

var topicFileRe = regexp.MustCompile(`^topics/[a-z0-9][a-z0-9_-]*\.md$`)

// memoryLocks serializes memory file writes per agent, and of the shared
// profile under profileLockKey.
var memoryLocks sync.Map

const profileLockKey = "\x00profile"

func lockMemory(key string) func() {
	v, _ := memoryLocks.LoadOrStore(key, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// memoryProposal is the ops column of a proposal row.
type memoryProposal struct {
	Kind   string               `json:"kind,omitempty"`
	Slug   string               `json:"slug,omitempty"` // routine proposals
	Text   string               `json:"text,omitempty"`
	Reason string               `json:"reason,omitempty"`
	Source string               `json:"source"`
	Ops    []api.PAgentMemoryOp `json:"ops"`
	Log    string               `json:"log,omitempty"`
}

// memoryResult is what one applied change produced.
type memoryResult struct {
	commit, profileCommit string
	applied               []memory.Op
	errs                  []error
}

func toAPIOps(ops []memory.Op) []api.PAgentMemoryOp {
	out := make([]api.PAgentMemoryOp, len(ops))
	for i, op := range ops {
		out[i] = api.PAgentMemoryOp(op)
	}
	return out
}

func fromAPIOps(ops []api.PAgentMemoryOp) []memory.Op {
	out := make([]memory.Op, len(ops))
	for i, op := range ops {
		out[i] = memory.Op(op)
	}
	return out
}

// memoryOpFile normalizes an op's file, relative to the memory directory
// (PROFILE.md names the shared profile).
func memoryOpFile(f string) (string, error) {
	switch f = strings.TrimPrefix(strings.TrimSpace(f), memoryDir+"/"); f {
	case "":
		return memoryFile, nil
	case memoryFile, userFile, profileFile:
		return f, nil
	}
	if topicFileRe.MatchString(f) {
		return f, nil
	}
	return "", fmt.Errorf("unknown memory file %q", f)
}

// opSecret reports the first field of op that looks like a secret.
func opSecret(op memory.Op) (rule, field string, bad bool) {
	for _, f := range [][2]string{{"text", op.Text}, {"until", op.Until}, {"match", op.Match}, {"file", op.File}, {"action", op.Action}} {
		if rule, bad := memory.LooksSecret(f[1]); bad {
			return rule, f[0], true
		}
	}
	return "", "", false
}

func (d *Node) pagentSettings(a pagent.Agent) pagent.Settings {
	def, _ := pagent.LoadDefinition(a.Dir)
	return pagent.Resolve(def, d.pagentCfg.Defaults)
}

// memoryEvent records a memory event in the thread, when there is one.
func (d *Node) memoryEvent(ctx context.Context, threadID, kind string, payload any) {
	if threadID == "" {
		return
	}
	d.logEvent(ctx, threadID, kind, payload, "")
}

// agentMemoryEvent records a memory event in threadID, or for a threadless
// change in the agent's most recently active chat thread; with neither it
// only logs.
func (d *Node) agentMemoryEvent(ctx context.Context, a pagent.Agent, threadID, kind string, payload any) {
	if threadID == "" {
		d.agentNotice(ctx, a, kind, payload)
		return
	}
	d.memoryEvent(ctx, threadID, kind, payload)
}

func joinErrs(errs []error) string {
	msgs := make([]string, len(errs))
	for i, e := range errs {
		msgs[i] = e.Error()
	}
	return strings.Join(msgs, "; ")
}

// pagentApplyMemory applies ops written by source (capture, agent, tidy) for
// the agent, following its memory mode. Secret-looking ops are dropped. In
// review mode, and for PROFILE.md always, ops are staged as a proposal;
// otherwise they are applied and committed with logLine appended to the work
// log. applied counts the ops committed.
func (d *Node) pagentApplyMemory(ctx context.Context, a pagent.Agent, threadID, source string, ops []memory.Op, logLine string) (applied int, err error) {
	return d.pagentApplyMemoryMode(ctx, a, threadID, source, ops, logLine, false)
}

// pagentApplyMemoryMode is pagentApplyMemory; forceReview stages every op as
// a proposal whatever the memory mode is (but not when memory is off).
func (d *Node) pagentApplyMemoryMode(ctx context.Context, a pagent.Agent, threadID, source string, ops []memory.Op, logLine string, forceReview bool) (applied int, err error) {
	set := d.pagentSettings(a)
	if forceReview && set.MemoryMode != memoryModeOff {
		set.MemoryMode = memoryModeReview
	}
	if set.MemoryMode == memoryModeOff {
		return 0, pagentErr("memory is off for this agent")
	}
	var direct, staged []memory.Op
	var errs []error
	blocked := ""
	for _, op := range ops {
		if rule, field, bad := opSecret(op); bad {
			d.agentMemoryEvent(ctx, a, threadID, evMemoryBlocked, map[string]string{
				"rule": rule, "field": field, "text": memory.MaskSecrets(op.Text),
			})
			blocked = rule
			continue
		}
		if err := memory.ValidateOp(op); err != nil {
			errs = append(errs, err)
			continue
		}
		f, err := memoryOpFile(op.File)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		op.File = f
		if set.MemoryMode == memoryModeReview || f == profileFile {
			staged = append(staged, op)
		} else {
			direct = append(direct, op)
		}
	}
	if rule, bad := memory.LooksSecret(logLine); bad {
		d.agentMemoryEvent(ctx, a, threadID, evMemoryBlocked, map[string]string{"rule": rule, "text": memory.MaskSecrets(logLine)})
		blocked, logLine = rule, ""
	}
	if len(errs) > 0 {
		d.agentMemoryEvent(ctx, a, threadID, evMemoryError, map[string]string{"message": joinErrs(errs)})
	}

	stagedLog := ""
	if set.MemoryMode == memoryModeReview {
		stagedLog, logLine = logLine, ""
	}
	if len(staged) > 0 || stagedLog != "" {
		if err := d.stageMemory(ctx, a, threadID, source, staged, stagedLog); err != nil {
			return 0, err
		}
	}
	if len(direct) > 0 || logLine != "" {
		res, err := d.commitMemory(ctx, a, set, threadID, source, direct, logLine)
		if err != nil {
			return 0, err
		}
		applied, errs = len(res.applied), append(errs, res.errs...)
	}
	if applied == 0 && len(staged) == 0 && stagedLog == "" && logLine == "" {
		switch {
		case len(errs) > 0:
			return 0, pagentErr("%s", joinErrs(errs))
		case blocked != "":
			return 0, pagentErr("looks like a secret (%s); not saved", blocked)
		}
	}
	return applied, nil
}

func (d *Node) stageMemory(ctx context.Context, a pagent.Agent, threadID, source string, ops []memory.Op, logLine string) error {
	body, err := json.Marshal(memoryProposal{Source: source, Ops: toAPIOps(ops), Log: logLine})
	if err != nil {
		return err
	}
	id := "mp_" + strings.TrimPrefix(pagent.NewID(), "ag_")
	if err := d.pagents.InsertProposal(ctx, pagent.Proposal{ID: id, AgentID: a.ID, ThreadID: threadID, Ops: body}); err != nil {
		return pagentErr("%v", err)
	}
	d.agentMemoryEvent(ctx, a, threadID, evMemoryProposed, map[string]any{"proposal_id": id, "ops": toAPIOps(ops)})
	d.notifyConns(api.MethodPAgentChanged)
	return nil
}

func (d *Node) memoryNow(set pagent.Settings) time.Time {
	now := d.pagentNow()
	if set.Timezone != "" {
		if loc, err := time.LoadLocation(set.Timezone); err == nil {
			now = now.In(loc)
		}
	}
	return now
}

func memoryTrailers(a pagent.Agent, source, threadID string) []agentgit.Trailer {
	return []agentgit.Trailer{
		{Key: "Argus-Agent", Value: a.Name},
		{Key: "Argus-Agent-Id", Value: a.ID},
		{Key: "Argus-Source", Value: source},
		{Key: "Argus-Thread", Value: threadID},
	}
}

func (d *Node) memorySubject(ctx context.Context, source, threadID string) string {
	if threadID == "" {
		return "memory: " + source
	}
	title := threadID
	if th, ok, _ := d.pagents.Thread(ctx, threadID); ok && th.Title != "" {
		title = th.Title
	}
	if r := []rune(title); len(r) > memoryTitleRunes {
		title = string(r[:memoryTitleRunes])
	}
	return fmt.Sprintf(`memory: %s from "%s"`, source, title)
}

// commitHandEdits commits the user's uncommitted edits under paths and warns
// in the thread when one looks like a secret.
func (d *Node) commitHandEdits(ctx context.Context, repo *agentgit.Repo, a pagent.Agent, threadID string, paths []string) error {
	hash, err := repo.CommitUserEdits(ctx, paths, a.Name, a.ID)
	if err != nil || hash == "" {
		return err
	}
	lines, err := repo.AddedLines(ctx, hash)
	if err != nil {
		d.log.Warn("pagent: read hand edit", "commit", hash, "err", err)
		return nil
	}
	for _, ln := range lines {
		if rule, bad := memory.LooksSecret(ln); bad {
			d.agentMemoryEvent(ctx, a, threadID, evMemoryError, map[string]string{
				"message": fmt.Sprintf("a hand edit looks like a secret (%s); it was committed and stays in history", rule),
				"commit":  hash,
			})
			break
		}
	}
	return nil
}

func readFileOptional(path string) (string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return string(data), err
}

func writeFileMkdir(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

// applyOpsToFiles applies ops grouped by file under dir, in order of first
// appearance, and writes the files that changed.
func applyOpsToFiles(dir string, ops []memory.Op, today string) (applied []memory.Op, changed map[string]bool, errs []error, err error) {
	var order []string
	byFile := map[string][]memory.Op{}
	for _, op := range ops {
		if _, seen := byFile[op.File]; !seen {
			order = append(order, op.File)
		}
		byFile[op.File] = append(byFile[op.File], op)
	}
	changed = map[string]bool{}
	for _, f := range order {
		path := filepath.Join(dir, f)
		content, err := readFileOptional(path)
		if err != nil {
			return nil, nil, nil, err
		}
		out, ok, opErrs := memory.Apply(content, byFile[f], today)
		for _, e := range opErrs {
			errs = append(errs, fmt.Errorf("%s: %w", f, e))
		}
		if len(ok) == 0 {
			continue
		}
		if err := writeFileMkdir(path, out); err != nil {
			return nil, nil, nil, err
		}
		applied = append(applied, ok...)
		changed[f] = true
	}
	return applied, changed, errs, nil
}

// archiveOverBudget moves the oldest MEMORY.md entries (by date, then line)
// to topics/archive.md until MEMORY.md fits budget. It returns how many moved.
func archiveOverBudget(memDir string, budget int64) (int, error) {
	if budget <= 0 {
		return 0, nil
	}
	path := filepath.Join(memDir, memoryFile)
	content, err := readFileOptional(path)
	if err != nil || !memory.OverBudget(content, budget) {
		return 0, err
	}
	header := memory.Header(content)
	entries := memory.Parse(content)
	oldest := slices.Clone(entries)
	slices.SortStableFunc(oldest, func(x, y memory.Entry) int {
		return cmp.Or(cmp.Compare(x.Date, y.Date), cmp.Compare(x.Line, y.Line))
	})
	moved := map[int]bool{}
	var archived []memory.Entry
	keep := entries
	for _, e := range oldest {
		if !memory.OverBudget(memory.Render(keep, header), budget) {
			break
		}
		moved[e.Line] = true
		archived = append(archived, e)
		keep = slices.DeleteFunc(slices.Clone(entries), func(x memory.Entry) bool { return moved[x.Line] })
	}
	if len(archived) == 0 {
		return 0, nil
	}
	archPath := filepath.Join(memDir, archiveFile)
	arch, err := readFileOptional(archPath)
	if err != nil {
		return 0, err
	}
	archHeader := memory.Header(arch)
	if strings.TrimSpace(arch) == "" {
		archHeader = "# Archive"
	}
	if err := writeFileMkdir(archPath, memory.Render(append(memory.Parse(arch), archived...), archHeader)); err != nil {
		return 0, err
	}
	return len(archived), writeFileMkdir(path, memory.Render(keep, header))
}

func appendWorkLog(memDir string, now time.Time, line string) error {
	line = strings.Join(strings.Fields(line), " ")
	path := filepath.Join(memDir, "log", now.Format("2006-01")+".md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "- %s %s\n", now.Format("2006-01-02 15:04"), line)
	return err
}

func memorySummary(applied []memory.Op, archived int, logged bool) string {
	counts := map[string]int{}
	for _, op := range applied {
		counts[op.Action]++
	}
	var parts []string
	plural := func(n int, one, many string) string {
		if n == 1 {
			return one
		}
		return many
	}
	if n := counts["add"]; n > 0 {
		parts = append(parts, fmt.Sprintf("saved %d %s", n, plural(n, "fact", "facts")))
	}
	if n := counts["update"]; n > 0 {
		parts = append(parts, fmt.Sprintf("updated %d", n))
	}
	if n := counts["remove"]; n > 0 {
		parts = append(parts, fmt.Sprintf("removed %d", n))
	}
	if archived > 0 {
		parts = append(parts, fmt.Sprintf("archived %d", archived))
	}
	if logged {
		parts = append(parts, "logged work")
	}
	return strings.Join(parts, ", ")
}

// commitMemory applies ops and logLine now and commits them: agent files in
// the agent's repository, PROFILE.md ops in the shared profile repository.
// The user's uncommitted edits are committed first.
func (d *Node) commitMemory(ctx context.Context, a pagent.Agent, set pagent.Settings, threadID, source string, ops []memory.Op, logLine string) (memoryResult, error) {
	var res memoryResult
	var agentOps, profileOps []memory.Op
	for _, op := range ops {
		if op.File == profileFile {
			profileOps = append(profileOps, op)
		} else {
			agentOps = append(agentOps, op)
		}
	}
	now := d.memoryNow(set)
	today := now.Format("2006-01-02")
	archived := 0
	logged := false

	// The profile goes first: the work log line is written only with the
	// agent commit, so retrying after a failure never logs it twice.
	if len(profileOps) > 0 {
		applied, errs, hash, err := d.commitProfile(ctx, a, threadID, source, profileOps, today)
		if err != nil {
			return res, err
		}
		res.applied = append(res.applied, applied...)
		res.errs = append(res.errs, errs...)
		res.profileCommit = hash
	}

	if len(agentOps) > 0 || logLine != "" {
		defer lockMemory(a.ID)()
		repo, err := agentgit.Open(ctx, a.Dir, d.pagentGitMode())
		if err != nil {
			return res, pagentErr("%v", err)
		}
		if err := d.commitHandEdits(ctx, repo, a, threadID, []string{memoryDir, pagent.DefinitionFile}); err != nil {
			return res, pagentErr("%v", err)
		}
		memDir := filepath.Join(a.Dir, memoryDir)
		applied, changed, errs, err := applyOpsToFiles(memDir, agentOps, today)
		if err != nil {
			return res, pagentErr("%v", err)
		}
		res.applied, res.errs = append(res.applied, applied...), append(res.errs, errs...)
		if changed[memoryFile] {
			if archived, err = archiveOverBudget(memDir, set.CoreBudget); err != nil {
				return res, pagentErr("%v", err)
			}
		}
		if logLine != "" {
			if err := appendWorkLog(memDir, now, logLine); err != nil {
				return res, pagentErr("%v", err)
			}
			logged = true
		}
		if len(applied) > 0 || logged {
			res.commit, err = repo.CommitPaths(ctx, d.memorySubject(ctx, source, threadID), memoryTrailers(a, source, threadID), []string{memoryDir}, true)
			if err != nil {
				return res, pagentErr("%v", err)
			}
		}
	}

	if len(res.errs) > 0 {
		d.agentMemoryEvent(ctx, a, threadID, evMemoryError, map[string]string{"message": joinErrs(res.errs)})
	}
	if res.commit != "" || res.profileCommit != "" {
		payload := map[string]any{
			"commit":  res.commit,
			"summary": memorySummary(res.applied, archived, logged),
			"ops":     toAPIOps(res.applied),
			"source":  source,
		}
		if res.profileCommit != "" {
			payload["profile_commit"] = res.profileCommit
		}
		d.agentMemoryEvent(ctx, a, threadID, evMemoryChanged, payload)
		d.notifyConns(api.MethodPAgentChanged)
	}
	return res, nil
}

// profileRepo opens the shared profile repository, creating it with an empty
// PROFILE.md on first use.
func (d *Node) profileRepo(ctx context.Context) (*agentgit.Repo, error) {
	dir := d.pagentCfg.ProfileDir
	if dir == "" {
		return nil, pagentErr("no profile directory is configured")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, pagentErr("%v", err)
	}
	path := filepath.Join(dir, profileFile)
	created := false
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			return nil, pagentErr("%v", err)
		}
		created = true
	}
	repo, err := agentgit.Open(ctx, dir, agentgit.ModeNested)
	if err != nil {
		return nil, pagentErr("%v", err)
	}
	if created {
		if _, err := repo.CommitPaths(ctx, "profile: create", []agentgit.Trailer{{Key: "Argus-Source", Value: "you"}}, []string{profileFile}, true); err != nil {
			return nil, pagentErr("%v", err)
		}
	}
	return repo, nil
}

func (d *Node) commitProfile(ctx context.Context, a pagent.Agent, threadID, source string, ops []memory.Op, today string) ([]memory.Op, []error, string, error) {
	defer lockMemory(profileLockKey)()
	repo, err := d.profileRepo(ctx)
	if err != nil {
		return nil, nil, "", err
	}
	if err := d.commitHandEdits(ctx, repo, a, threadID, []string{profileFile}); err != nil {
		return nil, nil, "", pagentErr("%v", err)
	}
	applied, _, errs, err := applyOpsToFiles(repo.Dir, ops, today)
	if err != nil {
		return nil, nil, "", pagentErr("%v", err)
	}
	if len(applied) == 0 {
		return nil, errs, "", nil
	}
	hash, err := repo.CommitPaths(ctx, d.memorySubject(ctx, source, threadID), memoryTrailers(a, source, threadID), []string{profileFile}, true)
	if err != nil {
		return nil, nil, "", pagentErr("%v", err)
	}
	return applied, errs, hash, nil
}

func (d *Node) handlePAgentMemoryShow(ctx context.Context, params json.RawMessage) (any, error) {
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
	memDir := filepath.Join(a.Dir, memoryDir)
	res := api.PAgentMemoryShowResult{Topics: []string{}, Budget: d.pagentSettings(a).CoreBudget}
	for _, f := range []struct {
		dst  *string
		path string
	}{
		{&res.Memory, filepath.Join(memDir, memoryFile)},
		{&res.User, filepath.Join(memDir, userFile)},
	} {
		if *f.dst, err = readFileOptional(f.path); err != nil {
			return nil, pagentErr("%v", err)
		}
	}
	if dir := d.pagentCfg.ProfileDir; dir != "" {
		if res.Profile, err = readFileOptional(filepath.Join(dir, profileFile)); err != nil {
			return nil, pagentErr("%v", err)
		}
	}
	topics, _ := filepath.Glob(filepath.Join(memDir, "topics", "*.md"))
	for _, t := range topics {
		res.Topics = append(res.Topics, filepath.Base(t))
	}
	slices.Sort(res.Topics)
	res.Usage = int64(len(res.Memory))
	return res, nil
}

// memoryRepo opens the repository and the paths a memory log or undo covers.
func (d *Node) memoryRepo(ctx context.Context, a pagent.Agent, profile bool) (*agentgit.Repo, []string, error) {
	if profile {
		repo, err := d.profileRepo(ctx)
		return repo, []string{profileFile}, err
	}
	repo, err := agentgit.Open(ctx, a.Dir, d.pagentGitMode())
	if err != nil {
		return nil, nil, pagentErr("%v", err)
	}
	return repo, []string{memoryDir}, nil
}

func (d *Node) handlePAgentMemoryLog(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.PAgentMemoryLogParams](params)
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
	repo, paths, err := d.memoryRepo(ctx, a, p.Profile)
	if err != nil {
		return nil, err
	}
	commits, err := repo.Log(ctx, paths, p.Limit)
	if err != nil {
		return nil, pagentErr("%v", err)
	}
	res := api.PAgentMemoryLogResult{Commits: make([]api.PAgentMemoryCommit, len(commits))}
	for i, c := range commits {
		res.Commits[i] = api.PAgentMemoryCommit{
			Hash: c.Hash, Subject: c.Subject, Source: c.Trailers["Argus-Source"],
			ThreadID: c.Trailers["Argus-Thread"], When: c.When,
		}
	}
	return res, nil
}

func (d *Node) handlePAgentMemoryUndo(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.PAgentMemoryUndoParams](params)
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
	want := strings.ToLower(strings.TrimSpace(p.Commit))
	if len(want) < 4 {
		return nil, pagentErr("commit is required")
	}
	lockKey := a.ID
	if p.Profile {
		lockKey = profileLockKey
	}
	defer lockMemory(lockKey)()
	repo, paths, err := d.memoryRepo(ctx, a, p.Profile)
	if err != nil {
		return nil, err
	}
	if !p.Profile {
		// Routine changes from agent tools carry an undo too.
		paths = append(paths, routinesDir)
	}
	commits, err := repo.Log(ctx, paths, 0)
	if err != nil {
		return nil, pagentErr("%v", err)
	}
	var target string
	for _, c := range commits {
		if strings.HasPrefix(c.Hash, want) {
			if target != "" {
				return nil, pagentErr("commit %s is ambiguous", p.Commit)
			}
			target = c.Hash
		}
	}
	if target == "" {
		return nil, pagentErr("commit %s is not a memory change", p.Commit)
	}
	userPaths := paths
	if !p.Profile {
		userPaths = []string{memoryDir, routinesDir, pagent.DefinitionFile}
	}
	if err := d.commitHandEdits(ctx, repo, a, "", userPaths); err != nil {
		return nil, pagentErr("%v", err)
	}
	hash, err := repo.Revert(ctx, target, []agentgit.Trailer{
		{Key: "Argus-Agent", Value: a.Name},
		{Key: "Argus-Agent-Id", Value: a.ID},
		{Key: "Argus-Source", Value: "you"},
	})
	switch {
	case errors.Is(err, agentgit.ErrConflict):
		return nil, pagentErr("can't undo: later changes touched the same lines")
	case errors.Is(err, agentgit.ErrBusy):
		return nil, pagentErr("can't undo: the repository has an operation in progress")
	case err != nil:
		return nil, pagentErr("%v", err)
	}
	d.notifyConns(api.MethodPAgentChanged)
	return api.PAgentMemoryUndoResult{Commit: hash}, nil
}

func (d *Node) handlePAgentMemoryProposals(ctx context.Context, params json.RawMessage) (any, error) {
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
	props, err := s.PendingProposals(ctx, a.ID)
	if err != nil {
		return nil, pagentErr("%v", err)
	}
	res := api.PAgentMemoryProposalsResult{Proposals: make([]api.PAgentMemoryProposal, 0, len(props))}
	for _, pr := range props {
		var body memoryProposal
		if err := json.Unmarshal(pr.Ops, &body); err != nil {
			d.log.Warn("pagent: bad proposal", "id", pr.ID, "err", err)
			continue
		}
		ops := body.Ops
		if ops == nil {
			ops = []api.PAgentMemoryOp{}
		}
		summary := ""
		if body.Kind == proposalKindRoutine {
			summary = routineProposalSummary(a, body)
		}
		res.Proposals = append(res.Proposals, api.PAgentMemoryProposal{
			ID: pr.ID, AgentID: pr.AgentID, ThreadID: pr.ThreadID, Source: body.Source,
			Kind: body.Kind, Slug: body.Slug, Summary: summary, Text: body.Text, Reason: body.Reason,
			Ops: ops, Log: body.Log, CreatedAt: pr.CreatedAt,
		})
	}
	return res, nil
}

func (d *Node) handlePAgentMemoryDecide(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.PAgentMemoryDecideParams](params)
	if err != nil {
		return nil, err
	}
	s, err := d.pagentStore()
	if err != nil {
		return nil, err
	}
	if p.ProposalID == "" {
		return nil, pagentErr("proposal_id is required")
	}
	// One decision at a time, so a proposal is never applied twice.
	defer lockMemory("\x00decide")()
	pr, ok, err := s.Proposal(ctx, p.ProposalID)
	if err != nil {
		return nil, pagentErr("%v", err)
	}
	if !ok {
		return nil, pagentErr("proposal not found")
	}
	if pr.Decision != "" {
		return nil, pagentErr("proposal was already %s", pr.Decision)
	}
	a, err := d.pagentFind(ctx, s, pr.AgentID)
	if err != nil {
		return nil, err
	}
	if !p.Accept {
		if err := s.DecideProposal(ctx, pr.ID, "rejected"); err != nil {
			return nil, pagentErr("%v", err)
		}
		d.notifyConns(api.MethodPAgentChanged)
		return api.PAgentMemoryDecideResult{}, nil
	}
	var body memoryProposal
	if err := json.Unmarshal(pr.Ops, &body); err != nil {
		return nil, pagentErr("bad proposal: %v", err)
	}
	if body.Kind == proposalKindInstructions || body.Kind == proposalKindRoutine {
		apply := d.applyInstructionsProposal
		if body.Kind == proposalKindRoutine {
			apply = d.applyRoutineProposal
		}
		hash, err := apply(ctx, a, pr, body)
		if err != nil {
			return nil, err
		}
		if err := s.DecideProposal(ctx, pr.ID, "accepted"); err != nil {
			return nil, pagentErr("%v", err)
		}
		d.notifyConns(api.MethodPAgentChanged)
		return api.PAgentMemoryDecideResult{Commit: hash}, nil
	}
	set := d.pagentSettings(a)
	if set.MemoryMode == memoryModeOff {
		return nil, pagentErr("memory is off for this agent")
	}
	res, err := d.commitMemory(ctx, a, set, pr.ThreadID, body.Source, fromAPIOps(body.Ops), body.Log)
	if err != nil {
		return nil, err
	}
	if len(res.applied) == 0 && body.Log == "" && len(res.errs) > 0 {
		return nil, pagentErr("can't apply: %s", joinErrs(res.errs))
	}
	if err := s.DecideProposal(ctx, pr.ID, "accepted"); err != nil {
		return nil, pagentErr("%v", err)
	}
	d.notifyConns(api.MethodPAgentChanged)
	out := api.PAgentMemoryDecideResult{Commit: res.commit, ProfileCommit: res.profileCommit, Applied: len(res.applied)}
	for _, e := range res.errs {
		out.Rejected = append(out.Rejected, e.Error())
	}
	return out, nil
}

const maxMemoryImportBytes = 256 << 10

func (d *Node) handlePAgentMemoryImport(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.PAgentMemoryImportParams](params)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.Content) == "" {
		return nil, pagentErr("nothing to import")
	}
	if len(p.Content) > maxMemoryImportBytes {
		return nil, pagentErr("import is larger than %d KB", maxMemoryImportBytes>>10)
	}
	s, err := d.pagentStore()
	if err != nil {
		return nil, err
	}
	a, err := d.pagentFind(ctx, s, p.AgentID)
	if err != nil {
		return nil, err
	}
	set := d.pagentSettings(a)
	if set.MemoryMode == memoryModeOff {
		return nil, pagentErr("memory is off for this agent")
	}
	ex := d.extractorFor(set)
	if ex == nil {
		return nil, pagentErr("no extractor available")
	}
	mem, user, topics, err := readMemoryContext(a)
	if err != nil {
		return nil, pagentErr("%v", err)
	}
	reply, err := ex.Extract(ctx, memory.BuildImportPrompt(mem, user, topics, p.Content))
	if err != nil {
		return nil, pagentErr("extract: %v", err)
	}
	res, err := memory.ParseCapture(reply)
	if err != nil {
		return nil, pagentErr("%v", err)
	}
	ops := make([]memory.Op, len(res.Ops))
	for i, o := range res.Ops {
		ops[i] = o.Op
	}
	if len(ops) == 0 {
		return api.PAgentMemoryImportResult{}, nil
	}
	if _, err := d.pagentApplyMemoryMode(ctx, a, "", "you", ops, "", true); err != nil {
		return nil, err
	}
	return api.PAgentMemoryImportResult{Staged: len(ops)}, nil
}
