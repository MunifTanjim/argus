package node

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/MunifTanjim/argus/internal/api"
	"github.com/MunifTanjim/argus/internal/atomicfile"
	"github.com/MunifTanjim/argus/internal/bot"
	"github.com/MunifTanjim/argus/internal/bot/memory"
	"github.com/MunifTanjim/argus/internal/botgit"
)

const sourceTidy = "tidy"

// botTidyEvery is how often bots are checked for their tidy time.
var botTidyEvery = time.Minute

func (d *Node) botTidyLoop(ctx context.Context) {
	t := time.NewTicker(botTidyEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			d.botTidyDue(ctx)
		}
	}
}

// tidyTime returns the moment of today's tidy in the bot's timezone, and
// the current time there.
func (d *Node) tidyTime(set bot.Settings) (at, now time.Time, ok bool) {
	now = d.memoryNow(set)
	clock, err := time.Parse("15:04", set.TidyAt)
	if err != nil {
		return time.Time{}, now, false
	}
	y, m, day := now.Date()
	return time.Date(y, m, day, clock.Hour(), clock.Minute(), 0, 0, now.Location()), now, true
}

// botTidyDue tidies each bot whose tidy time has passed since it last
// ran. A bot whose time passed while the node was down runs once on the
// first check afterwards.
func (d *Node) botTidyDue(ctx context.Context) {
	bots, err := d.bots.ListBots(ctx, false)
	if err != nil {
		d.log.Warn("bot: list bots", "err", err)
		return
	}
	for _, a := range bots {
		if ctx.Err() != nil {
			return
		}
		set := d.botSettings(a)
		at, now, ok := d.tidyTime(set)
		if !ok || now.Before(at) {
			continue
		}
		d.tidyMu.Lock()
		if !d.tidyAt[a.ID].Before(at) || now.Sub(d.tidyTry[a.ID]) < captureErrEvery {
			d.tidyMu.Unlock()
			continue
		}
		if d.tidyTry == nil {
			d.tidyTry = map[string]time.Time{}
		}
		d.tidyTry[a.ID] = now
		d.tidyMu.Unlock()
		err := d.botTidy(ctx, a, false)
		if err == nil {
			d.tidyMu.Lock()
			if d.tidyAt == nil {
				d.tidyAt = map[string]time.Time{}
			}
			d.tidyAt[a.ID] = now
			d.tidyMu.Unlock()
		} else {
			d.log.Warn("bot: tidy memory", "bot", a.ID, "err", err)
			d.botMemoryEvent(ctx, a, "", evMemoryError, map[string]string{"message": "tidy failed: " + err.Error()})
		}
	}
}

// botTidyNow tidies the bot's memory immediately, whether or not it has
// new entries.
func (d *Node) botTidyNow(ctx context.Context, a bot.Bot) error {
	return d.botTidy(ctx, a, true)
}

// newEntriesSince reports whether memory has a capture or bot commit newer
// than the last tidy commit.
func newEntriesSince(ctx context.Context, repo *botgit.Repo) (bool, error) {
	commits, err := repo.Log(ctx, []string{memoryDir}, 0)
	if err != nil {
		return false, err
	}
	for _, c := range commits {
		switch c.Trailers["Argus-Source"] {
		case sourceTidy:
			return false, nil
		case "capture", "bot":
			return true, nil
		}
	}
	return false, nil
}

// botTidy runs one tidy pass: it drops expired entries, asks the extractor
// for merges and moves, checks the guards and commits the result as one tidy
// commit. A guard violation changes nothing and records a memory.error event.
func (d *Node) botTidy(ctx context.Context, a bot.Bot, force bool) error {
	set := d.botSettings(a)
	if set.MemoryMode != "" && set.MemoryMode != "auto" {
		return nil
	}
	defer lockMemory(a.ID)()
	repo, err := d.openBotRepo(ctx, a)
	if err != nil {
		return err
	}
	if err := d.commitHandEdits(ctx, repo, a, "", []string{memoryDir, bot.DefinitionFile}); err != nil {
		return err
	}
	head := ""
	if cs, err := repo.Log(ctx, []string{memoryDir}, 1); err != nil {
		return err
	} else if len(cs) > 0 {
		head = cs[0].Hash
	}
	if !force {
		if head != "" && d.tidyIdleHead(a.ID) == head {
			return nil
		}
		due, err := newEntriesSince(ctx, repo)
		if err != nil || !due {
			return err
		}
	}
	memDir := filepath.Join(a.Dir, memoryDir)
	content, err := readFileOptional(filepath.Join(memDir, memoryFile))
	if err != nil {
		return err
	}
	if len(memory.Parse(content)) == 0 {
		d.setTidyIdleHead(a.ID, head)
		return nil
	}
	now := d.memoryNow(set)
	today := now.Format("2006-01-02")

	var ops []memory.Op
	if len(memory.Parse(memory.DropLines(content, memory.ExpiredEntries(content, today)))) > 0 {
		if ops, err = d.tidyOps(ctx, set, memDir, content); err != nil {
			return err
		}
	}
	for _, op := range ops {
		if rule, field, bad := opSecret(op); bad {
			d.botMemoryEvent(ctx, a, "", evMemoryBlocked, map[string]string{"rule": rule, "field": field, "text": memory.MaskSecrets(op.Text)})
			return nil
		}
	}
	norm := ops[:0:0]
	for _, op := range ops {
		if err := memory.ValidateOp(op); err != nil {
			return err
		}
		f, err := memoryOpFile(op.File)
		if err != nil {
			return err
		}
		op.File = f
		norm = append(norm, op)
	}

	sources, err := repo.BlameSources(ctx, filepath.ToSlash(filepath.Join(memoryDir, memoryFile)))
	if err != nil {
		return err
	}
	userLines := map[int]bool{}
	for line, src := range sources {
		if src == "you" {
			userLines[line] = true
		}
	}
	plan, err := memory.PlanTidy(content, norm, today, userLines)
	if err != nil {
		d.botMemoryEvent(ctx, a, "", evMemoryError, map[string]string{"message": "tidy rejected: " + err.Error()})
		d.setTidyIdleHead(a.ID, head)
		return nil
	}
	if plan.Expired == 0 && len(plan.Applied) == 0 && len(plan.Topics) == 0 {
		d.setTidyIdleHead(a.ID, head)
		return nil
	}
	snap, err := snapshotFiles(memDir)
	if err != nil {
		return err
	}
	if err := writeFileMkdir(filepath.Join(memDir, memoryFile), plan.Memory); err != nil {
		return d.undoWrite(snap, err)
	}
	topicApplied, _, topicErrs, err := applyOpsToFiles(memDir, plan.Topics, today)
	if err != nil {
		return d.undoWrite(snap, err)
	}
	errs := append(plan.Errs, topicErrs...)
	hash, err := repo.CommitPaths(ctx, "memory: tidy", memoryTrailers(a, sourceTidy, ""), []string{memoryDir}, true)
	if err != nil {
		return d.undoWrite(snap, err)
	}
	if len(errs) > 0 {
		d.botMemoryEvent(ctx, a, "", evMemoryError, map[string]string{"message": joinErrs(errs)})
	}
	if hash != "" {
		applied := append(slices.Clone(plan.Applied), topicApplied...)
		summary := memorySummary(applied, 0, false)
		if plan.Expired > 0 {
			if summary != "" {
				summary += ", "
			}
			summary += fmt.Sprintf("expired %d", plan.Expired)
		}
		d.botMemoryEvent(ctx, a, "", evMemoryChanged, map[string]any{
			"commit":  hash,
			"summary": summary,
			"ops":     toAPIOps(applied),
			"source":  sourceTidy,
		})
		d.notifyConns(api.MethodBotChanged)
	}
	return nil
}

func (d *Node) tidyOps(ctx context.Context, set bot.Settings, memDir, content string) ([]memory.Op, error) {
	ex := d.extractorFor(set)
	if ex == nil {
		if _, err := memory.NewExtractor(set.Extractor); set.Extractor != "" && err != nil {
			return nil, fmt.Errorf("invalid memory.extractor: %s", set.Extractor)
		}
		return nil, errors.New("no extractor available")
	}
	var topics []string
	files, _ := filepath.Glob(filepath.Join(memDir, "topics", "*.md"))
	for _, f := range files {
		topics = append(topics, "topics/"+filepath.Base(f))
	}
	slices.Sort(topics)
	reply, err := ex.Extract(ctx, memory.BuildTidyPrompt(content, topics))
	if err != nil {
		return nil, err
	}
	ops, _, err := memory.ParseCaptureResult(reply)
	return ops, err
}

func (d *Node) tidyIdleFile(botID string) string {
	return filepath.Join(filepath.Dir(d.botPrimaryFile), "bot-tidy-"+botID)
}

// tidyIdleHead returns the last memory commit at which a tidy attempt changed
// nothing, or "".
func (d *Node) tidyIdleHead(botID string) string {
	d.tidyMu.Lock()
	defer d.tidyMu.Unlock()
	if h, ok := d.tidyIdle[botID]; ok {
		return h
	}
	data, _ := os.ReadFile(d.tidyIdleFile(botID))
	h := strings.TrimSpace(string(data))
	if d.tidyIdle == nil {
		d.tidyIdle = map[string]string{}
	}
	d.tidyIdle[botID] = h
	return h
}

func (d *Node) setTidyIdleHead(botID, head string) {
	if head == "" {
		return
	}
	d.tidyMu.Lock()
	if d.tidyIdle == nil {
		d.tidyIdle = map[string]string{}
	}
	d.tidyIdle[botID] = head
	d.tidyMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(d.tidyIdleFile(botID)), 0o755); err == nil {
		if err := atomicfile.Write(d.tidyIdleFile(botID), []byte(head)); err != nil {
			d.log.Warn("bot: save tidy state", "bot", botID, "err", err)
		}
	}
}
