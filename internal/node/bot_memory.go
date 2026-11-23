package node

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/bot"
	"github.com/MunifTanjim/argus/internal/bot/memory"
	"github.com/MunifTanjim/argus/internal/botgit"
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
	evMemoryUndone   = "memory.undone"
)

var topicFileRe = regexp.MustCompile(`^topics/[a-z0-9][a-z0-9_-]*\.md$`)

// memoryLocks serializes memory file writes per bot, and of the shared
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
	Kind   string            `json:"kind,omitempty"`
	Slug   string            `json:"slug,omitempty"` // routine proposals
	Text   string            `json:"text,omitempty"`
	Reason string            `json:"reason,omitempty"`
	Source string            `json:"source"`
	Ops    []api.BotMemoryOp `json:"ops"`
	Log    string            `json:"log,omitempty"`
}

// memoryResult is what one applied change produced.
type memoryResult struct {
	commit, profileCommit string
	applied               []memory.Op
	errs                  []error
}

func toAPIOps(ops []memory.Op) []api.BotMemoryOp {
	out := make([]api.BotMemoryOp, len(ops))
	for i, op := range ops {
		out[i] = api.BotMemoryOp(op)
	}
	return out
}

func fromAPIOps(ops []api.BotMemoryOp) []memory.Op {
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

func (d *Node) botSettings(a bot.Bot) bot.Settings {
	def, _ := bot.LoadDefinition(a.Dir)
	return bot.Resolve(def, d.botCfg.Defaults)
}

// memoryEvent records a memory event in the thread, when there is one.
func (d *Node) memoryEvent(ctx context.Context, threadID, kind string, payload any) {
	if threadID == "" {
		return
	}
	d.logEvent(ctx, threadID, kind, payload, "")
}

// botMemoryEvent records a memory event in threadID, or for a threadless
// change in the bot's most recently active chat thread; with neither it
// only logs.
func (d *Node) botMemoryEvent(ctx context.Context, a bot.Bot, threadID, kind string, payload any) {
	if threadID == "" {
		d.botNotice(ctx, a, kind, payload)
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

// botApplyMemory applies ops written by source (capture, bot, tidy) for
// the bot, following its memory mode. Secret-looking ops are dropped. In
// review mode, and for PROFILE.md always, ops are staged as a proposal;
// otherwise they are applied and committed with logLine appended to the work
// log. applied counts the ops committed.
func (d *Node) botApplyMemory(ctx context.Context, a bot.Bot, threadID, source string, ops []memory.Op, logLine string) (applied int, err error) {
	return d.botApplyMemoryMode(ctx, a, threadID, source, ops, logLine, false)
}

// botApplyMemoryMode is botApplyMemory; forceReview stages every op as
// a proposal whatever the memory mode is (but not when memory is off).
func (d *Node) botApplyMemoryMode(ctx context.Context, a bot.Bot, threadID, source string, ops []memory.Op, logLine string, forceReview bool) (applied int, err error) {
	set := d.botSettings(a)
	if forceReview && set.MemoryMode != memoryModeOff {
		set.MemoryMode = memoryModeReview
	}
	if set.MemoryMode == memoryModeOff {
		return 0, botErr("memory is off for this bot")
	}
	var direct, staged []memory.Op
	var errs []error
	blocked := ""
	for _, op := range ops {
		if rule, field, bad := opSecret(op); bad {
			d.botMemoryEvent(ctx, a, threadID, evMemoryBlocked, map[string]string{
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
		d.botMemoryEvent(ctx, a, threadID, evMemoryBlocked, map[string]string{"rule": rule, "text": memory.MaskSecrets(logLine)})
		blocked, logLine = rule, ""
	}
	if len(errs) > 0 {
		d.botMemoryEvent(ctx, a, threadID, evMemoryError, map[string]string{"message": joinErrs(errs)})
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
			return 0, botErr("%s", joinErrs(errs))
		case blocked != "":
			return 0, botErr("looks like a secret (%s); not saved", blocked)
		}
	}
	return applied, nil
}

func (d *Node) stageMemory(ctx context.Context, a bot.Bot, threadID, source string, ops []memory.Op, logLine string) error {
	body, err := json.Marshal(memoryProposal{Source: source, Ops: toAPIOps(ops), Log: logLine})
	if err != nil {
		return err
	}
	id := "mp_" + strings.TrimPrefix(bot.NewID(), "bot_")
	if err := d.bots.InsertProposal(ctx, bot.Proposal{ID: id, BotID: a.ID, ThreadID: threadID, Ops: body}); err != nil {
		return botErr("%v", err)
	}
	d.botMemoryEvent(ctx, a, threadID, evMemoryProposed, map[string]any{"proposal_id": id, "ops": toAPIOps(ops)})
	d.notifyConns(api.MethodBotChanged)
	return nil
}

func (d *Node) memoryNow(set bot.Settings) time.Time {
	now := d.botNow()
	if set.Timezone != "" {
		if loc, err := time.LoadLocation(set.Timezone); err == nil {
			now = now.In(loc)
		}
	}
	return now
}

func memoryTrailers(a bot.Bot, source, threadID string) []botgit.Trailer {
	return []botgit.Trailer{
		{Key: "Argus-Bot", Value: a.Name},
		{Key: "Argus-Bot-Id", Value: a.ID},
		{Key: "Argus-Source", Value: source},
		{Key: "Argus-Thread", Value: threadID},
	}
}

func (d *Node) memorySubject(ctx context.Context, source, threadID string) string {
	if threadID == "" {
		return "memory: " + source
	}
	title := threadID
	if th, ok, _ := d.bots.Thread(ctx, threadID); ok && th.Title != "" {
		title = th.Title
	}
	if r := []rune(title); len(r) > memoryTitleRunes {
		title = string(r[:memoryTitleRunes])
	}
	return fmt.Sprintf(`memory: %s from "%s"`, source, title)
}

// commitHandEdits commits the user's uncommitted edits under paths and warns
// in the thread when one looks like a secret.
func (d *Node) commitHandEdits(ctx context.Context, repo *botgit.Repo, a bot.Bot, threadID string, paths []string) error {
	hash, err := repo.CommitUserEdits(ctx, paths, a.Name, a.ID)
	if err != nil || hash == "" {
		return err
	}
	lines, err := repo.AddedLines(ctx, hash)
	if err != nil {
		d.log.Warn("bot: read hand edit", "commit", hash, "err", err)
		return nil
	}
	for _, ln := range lines {
		if rule, bad := memory.LooksSecret(ln); bad {
			d.botMemoryEvent(ctx, a, threadID, evMemoryError, map[string]string{
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

// fileSnapshot holds the files under a path (a file or a directory) so a
// change that fails to commit can be undone: left in place, it would be
// committed later as the user's own edit.
type fileSnapshot struct {
	root  string
	files map[string][]byte
}

func snapshotFiles(root string) (*fileSnapshot, error) {
	s := &fileSnapshot{root: root, files: map[string][]byte{}}
	err := filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil || !e.Type().IsRegular() {
			return err
		}
		data, err := os.ReadFile(p)
		s.files[p] = data
		return err
	})
	return s, err
}

// restore puts the files back as they were and removes files added since.
func (s *fileSnapshot) restore() error {
	var errs []error
	_ = filepath.WalkDir(s.root, func(p string, e fs.DirEntry, err error) error {
		if err == nil && e.Type().IsRegular() {
			if _, ok := s.files[p]; !ok {
				errs = append(errs, os.Remove(p))
			}
		}
		return nil
	})
	for p, data := range s.files {
		if cur, err := os.ReadFile(p); err == nil && bytes.Equal(cur, data) {
			continue
		}
		errs = append(errs, writeFileMkdir(p, string(data)))
	}
	return errors.Join(errs...)
}

// undoWrite restores snap after a failed write or commit and returns err as
// a retryable error: the same change can succeed once the cause is gone.
func (d *Node) undoWrite(snap *fileSnapshot, err error) error {
	if rerr := snap.restore(); rerr != nil {
		d.log.Warn("bot: undo memory write", "path", snap.root, "err", rerr)
	}
	return memoryWriteErr(err)
}

// memoryWriteErr marks a failure to write or commit memory as retryable,
// unlike botErr's rejections.
func memoryWriteErr(err error) error {
	return &api.RPCError{Code: api.CodeInternalError, Message: err.Error()}
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

// commitMemory applies ops and logLine now and commits them: bot files in
// the bot's repository, PROFILE.md ops in the shared profile repository.
// The user's uncommitted edits are committed first.
func (d *Node) commitMemory(ctx context.Context, a bot.Bot, set bot.Settings, threadID, source string, ops []memory.Op, logLine string) (memoryResult, error) {
	var res memoryResult
	var botOps, profileOps []memory.Op
	for _, op := range ops {
		if op.File == profileFile {
			profileOps = append(profileOps, op)
		} else {
			botOps = append(botOps, op)
		}
	}
	now := d.memoryNow(set)
	today := now.Format("2006-01-02")
	archived := 0
	logged := false

	// The profile goes first: the work log line is written only with the
	// bot commit, so retrying after a failure never logs it twice.
	if len(profileOps) > 0 {
		applied, errs, hash, err := d.commitProfile(ctx, a, threadID, source, profileOps, today)
		if err != nil {
			return res, err
		}
		res.applied = append(res.applied, applied...)
		res.errs = append(res.errs, errs...)
		res.profileCommit = hash
	}

	if len(botOps) > 0 || logLine != "" {
		defer lockMemory(a.ID)()
		repo, err := d.openBotRepo(ctx, a)
		if err != nil {
			return res, botErr("%v", err)
		}
		if err := d.commitHandEdits(ctx, repo, a, threadID, []string{memoryDir, bot.DefinitionFile}); err != nil {
			return res, handEditErr(err)
		}
		memDir := filepath.Join(a.Dir, memoryDir)
		snap, err := snapshotFiles(memDir)
		if err != nil {
			return res, memoryWriteErr(err)
		}
		applied, changed, errs, err := applyOpsToFiles(memDir, botOps, today)
		if err != nil {
			return res, d.undoWrite(snap, err)
		}
		res.applied, res.errs = append(res.applied, applied...), append(res.errs, errs...)
		if changed[memoryFile] {
			if archived, err = archiveOverBudget(memDir, set.CoreBudget); err != nil {
				return res, d.undoWrite(snap, err)
			}
		}
		if logLine != "" {
			if err := appendWorkLog(memDir, now, logLine); err != nil {
				return res, d.undoWrite(snap, err)
			}
			logged = true
		}
		if len(applied) > 0 || logged {
			res.commit, err = repo.CommitPaths(ctx, d.memorySubject(ctx, source, threadID), memoryTrailers(a, source, threadID), []string{memoryDir}, true)
			if err != nil {
				return res, d.undoWrite(snap, err)
			}
		}
	}

	if len(res.errs) > 0 {
		d.botMemoryEvent(ctx, a, threadID, evMemoryError, map[string]string{"message": joinErrs(res.errs)})
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
		d.botMemoryEvent(ctx, a, threadID, evMemoryChanged, payload)
		d.notifyConns(api.MethodBotChanged)
	}
	return res, nil
}

// profileRepo opens the shared profile repository, creating it with an empty
// PROFILE.md on first use.
func (d *Node) profileRepo(ctx context.Context, a bot.Bot) (*botgit.Repo, error) {
	dir := d.botCfg.ProfileDir
	if dir == "" {
		return nil, botErr("no profile directory is configured")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, botErr("%v", err)
	}
	path := filepath.Join(dir, profileFile)
	created := false
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			return nil, botErr("%v", err)
		}
		created = true
	}
	repo, err := botgit.Open(ctx, dir, botgit.ModeNested)
	if err != nil {
		return nil, botErr("%v", err)
	}
	d.guardProfileRepo(ctx, a, repo)
	if created {
		if _, err := repo.CommitPaths(ctx, "profile: create", []botgit.Trailer{{Key: "Argus-Source", Value: "you"}}, []string{profileFile}, true); err != nil {
			return nil, botErr("%v", err)
		}
	}
	return repo, nil
}

func (d *Node) commitProfile(ctx context.Context, a bot.Bot, threadID, source string, ops []memory.Op, today string) ([]memory.Op, []error, string, error) {
	defer lockMemory(profileLockKey)()
	repo, err := d.profileRepo(ctx, a)
	if err != nil {
		return nil, nil, "", err
	}
	if err := d.commitHandEdits(ctx, repo, a, threadID, []string{profileFile}); err != nil {
		return nil, nil, "", handEditErr(err)
	}
	snap, err := snapshotFiles(filepath.Join(repo.Dir, profileFile))
	if err != nil {
		return nil, nil, "", memoryWriteErr(err)
	}
	applied, _, errs, err := applyOpsToFiles(repo.Dir, ops, today)
	if err != nil {
		return nil, nil, "", d.undoWrite(snap, err)
	}
	if len(applied) == 0 {
		return nil, errs, "", nil
	}
	hash, err := repo.CommitPaths(ctx, d.memorySubject(ctx, source, threadID), memoryTrailers(a, source, threadID), []string{profileFile}, true)
	if err != nil {
		return nil, nil, "", d.undoWrite(snap, err)
	}
	return applied, errs, hash, nil
}

func (d *Node) handleBotMemoryShow(ctx context.Context, params json.RawMessage) (any, error) {
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
	memDir := filepath.Join(a.Dir, memoryDir)
	res := api.BotMemoryShowResult{Topics: []string{}, Budget: d.botSettings(a).CoreBudget}
	for _, f := range []struct {
		dst  *string
		path string
	}{
		{&res.Memory, filepath.Join(memDir, memoryFile)},
		{&res.User, filepath.Join(memDir, userFile)},
	} {
		if *f.dst, err = readFileOptional(f.path); err != nil {
			return nil, botErr("%v", err)
		}
	}
	if dir := d.botCfg.ProfileDir; dir != "" {
		if res.Profile, err = readFileOptional(filepath.Join(dir, profileFile)); err != nil {
			return nil, botErr("%v", err)
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
func (d *Node) memoryRepo(ctx context.Context, a bot.Bot, profile bool) (*botgit.Repo, []string, error) {
	if profile {
		repo, err := d.profileRepo(ctx, a)
		return repo, []string{profileFile}, err
	}
	repo, err := d.openBotRepo(ctx, a)
	if err != nil {
		return nil, nil, botErr("%v", err)
	}
	return repo, []string{memoryDir}, nil
}

func (d *Node) handleBotMemoryLog(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.BotMemoryLogParams](params)
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
	repo, paths, err := d.memoryRepo(ctx, a, p.Profile)
	if err != nil {
		return nil, err
	}
	commits, err := repo.Log(ctx, paths, p.Limit)
	if err != nil {
		return nil, botErr("%v", err)
	}
	res := api.BotMemoryLogResult{Commits: make([]api.BotMemoryCommit, len(commits))}
	for i, c := range commits {
		res.Commits[i] = api.BotMemoryCommit{
			Hash: c.Hash, Subject: c.Subject, Source: c.Trailers["Argus-Source"],
			ThreadID: c.Trailers["Argus-Thread"], When: c.When,
		}
	}
	return res, nil
}

// changedOutside returns a file that commit hash of the repository enclosing
// dir changes outside dir, or "" when it changes only files within dir.
func changedOutside(ctx context.Context, dir, hash string) (string, error) {
	git := func(args ...string) (string, error) {
		out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).Output()
		if err != nil {
			return "", fmt.Errorf("git %s: %w", args[0], err)
		}
		return string(out), nil
	}
	prefix, err := git("rev-parse", "--show-prefix")
	if err != nil {
		return "", err
	}
	prefix = strings.TrimSpace(prefix)
	names, err := git("diff-tree", "--no-commit-id", "--name-only", "-r", "-z", "--root", "--end-of-options", hash)
	if err != nil {
		return "", err
	}
	for _, name := range strings.Split(names, "\x00") {
		if name != "" && !strings.HasPrefix(name, prefix) {
			return name, nil
		}
	}
	return "", nil
}

func (d *Node) handleBotMemoryUndo(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.BotMemoryUndoParams](params)
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
	want := strings.ToLower(strings.TrimSpace(p.Commit))
	if len(want) < 4 {
		return nil, botErr("commit is required")
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
		// Routine changes from bot tools carry an undo too.
		paths = append(paths, routinesDir)
	}
	commits, err := repo.Log(ctx, paths, 0)
	if err != nil {
		return nil, botErr("%v", err)
	}
	var target string
	for _, c := range commits {
		if strings.HasPrefix(c.Hash, want) {
			if target != "" {
				return nil, botErr("commit %s is ambiguous", p.Commit)
			}
			target = c.Hash
		}
	}
	if target == "" {
		return nil, botErr("commit %s is not a memory change", p.Commit)
	}
	if repo.Outer {
		outside, err := changedOutside(ctx, a.Dir, target)
		if err != nil {
			return nil, botErr("%v", err)
		}
		if outside != "" {
			return nil, botErr("can't undo: commit %s also changes %s, outside the bot's directory", p.Commit, outside)
		}
	}
	userPaths := paths
	if !p.Profile {
		userPaths = []string{memoryDir, routinesDir, bot.DefinitionFile}
	}
	if err := d.commitHandEdits(ctx, repo, a, "", userPaths); err != nil {
		return nil, botErr("%v", err)
	}
	hash, err := repo.Revert(ctx, target, []botgit.Trailer{
		{Key: "Argus-Bot", Value: a.Name},
		{Key: "Argus-Bot-Id", Value: a.ID},
		{Key: "Argus-Source", Value: "you"},
	})
	switch {
	case errors.Is(err, botgit.ErrConflict):
		return nil, botErr("can't undo: later changes touched the same lines")
	case errors.Is(err, botgit.ErrBusy):
		return nil, botErr("can't undo: the repository has an operation in progress")
	case err != nil:
		return nil, botErr("%v", err)
	}
	if p.ThreadID != "" {
		if th, ok, err := s.Thread(ctx, p.ThreadID); err == nil && ok && th.BotID == a.ID {
			d.logEvent(ctx, th.ID, evMemoryUndone, map[string]string{"commit": target, "revert": hash}, "")
		}
	}
	d.notifyConns(api.MethodBotChanged)
	return api.BotMemoryUndoResult{Commit: hash}, nil
}

func (d *Node) handleBotMemoryProposals(ctx context.Context, params json.RawMessage) (any, error) {
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
	props, err := s.PendingProposals(ctx, a.ID)
	if err != nil {
		return nil, botErr("%v", err)
	}
	res := api.BotMemoryProposalsResult{Proposals: make([]api.BotMemoryProposal, 0, len(props))}
	for _, pr := range props {
		var body memoryProposal
		if err := json.Unmarshal(pr.Ops, &body); err != nil {
			d.log.Warn("bot: bad proposal", "id", pr.ID, "err", err)
			continue
		}
		ops := body.Ops
		if ops == nil {
			ops = []api.BotMemoryOp{}
		}
		summary := ""
		switch body.Kind {
		case proposalKindRoutine:
			summary = routineProposalSummary(a, body)
		case proposalKindBot:
			summary = botProposalSummary(body)
		}
		res.Proposals = append(res.Proposals, api.BotMemoryProposal{
			ID: pr.ID, BotID: pr.BotID, ThreadID: pr.ThreadID, Source: body.Source,
			Kind: body.Kind, Slug: body.Slug, Summary: summary, Text: body.Text, Reason: body.Reason,
			Ops: ops, Log: body.Log, CreatedAt: pr.CreatedAt,
		})
	}
	return res, nil
}

func (d *Node) handleBotMemoryDecide(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.BotMemoryDecideParams](params)
	if err != nil {
		return nil, err
	}
	s, err := d.botStore()
	if err != nil {
		return nil, err
	}
	if p.ProposalID == "" {
		return nil, botErr("proposal_id is required")
	}
	// One decision at a time, so a proposal is never applied twice.
	defer lockMemory("\x00decide")()
	pr, ok, err := s.Proposal(ctx, p.ProposalID)
	if err != nil {
		return nil, botErr("%v", err)
	}
	if !ok {
		return nil, botErr("proposal not found")
	}
	if pr.Decision != "" {
		return nil, botErr("proposal was already %s", pr.Decision)
	}
	a, err := d.botFind(ctx, s, pr.BotID)
	if err != nil {
		return nil, err
	}
	if !p.Accept {
		if err := s.DecideProposal(ctx, pr.ID, "rejected"); err != nil {
			return nil, botErr("%v", err)
		}
		d.notifyConns(api.MethodBotChanged)
		return api.BotMemoryDecideResult{}, nil
	}
	var body memoryProposal
	if err := json.Unmarshal(pr.Ops, &body); err != nil {
		return nil, botErr("bad proposal: %v", err)
	}
	if body.Kind == proposalKindBot {
		if _, err := d.applyBotProposal(ctx, body); err != nil {
			return nil, err
		}
		if err := s.DecideProposal(ctx, pr.ID, "accepted"); err != nil {
			return nil, botErr("%v", err)
		}
		d.notifyConns(api.MethodBotChanged)
		return api.BotMemoryDecideResult{}, nil
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
			return nil, botErr("%v", err)
		}
		d.notifyConns(api.MethodBotChanged)
		return api.BotMemoryDecideResult{Commit: hash}, nil
	}
	set := d.botSettings(a)
	if set.MemoryMode == memoryModeOff {
		return nil, botErr("memory is off for this bot")
	}
	res, err := d.commitMemory(ctx, a, set, pr.ThreadID, body.Source, fromAPIOps(body.Ops), body.Log)
	if err != nil {
		return nil, err
	}
	if len(res.applied) == 0 && body.Log == "" && len(res.errs) > 0 {
		return nil, botErr("can't apply: %s", joinErrs(res.errs))
	}
	if err := s.DecideProposal(ctx, pr.ID, "accepted"); err != nil {
		return nil, botErr("%v", err)
	}
	d.notifyConns(api.MethodBotChanged)
	out := api.BotMemoryDecideResult{Commit: res.commit, ProfileCommit: res.profileCommit, Applied: len(res.applied)}
	for _, e := range res.errs {
		out.Rejected = append(out.Rejected, e.Error())
	}
	return out, nil
}

const maxMemoryImportBytes = 256 << 10

func (d *Node) handleBotMemoryImport(ctx context.Context, params json.RawMessage) (any, error) {
	p, err := api.Decode[api.BotMemoryImportParams](params)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.Content) == "" {
		return nil, botErr("nothing to import")
	}
	if len(p.Content) > maxMemoryImportBytes {
		return nil, botErr("import is larger than %d KB", maxMemoryImportBytes>>10)
	}
	s, err := d.botStore()
	if err != nil {
		return nil, err
	}
	a, err := d.botFind(ctx, s, p.BotID)
	if err != nil {
		return nil, err
	}
	set := d.botSettings(a)
	if set.MemoryMode == memoryModeOff {
		return nil, botErr("memory is off for this bot")
	}
	ex := d.extractorFor(set)
	if ex == nil {
		return nil, botErr("no extractor available")
	}
	mem, user, topics, err := readMemoryContext(a)
	if err != nil {
		return nil, botErr("%v", err)
	}
	reply, err := ex.Extract(ctx, memory.BuildImportPrompt(mem, user, topics, p.Content))
	if err != nil {
		return nil, botErr("extract: %v", err)
	}
	res, err := memory.ParseCapture(reply)
	if err != nil {
		return nil, botErr("%v", err)
	}
	ops := make([]memory.Op, len(res.Ops))
	for i, o := range res.Ops {
		ops[i] = o.Op
	}
	if len(ops) == 0 {
		return api.BotMemoryImportResult{}, nil
	}
	if _, err := d.botApplyMemoryMode(ctx, a, "", "you", ops, "", true); err != nil {
		return nil, err
	}
	return api.BotMemoryImportResult{Staged: len(ops)}, nil
}
