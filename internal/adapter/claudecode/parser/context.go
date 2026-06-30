package parser

import "strings"

// Context-window sizes in tokens. When Anthropic ships a new 1M model,
// add a line to largeContextModels.
const (
	defaultContextWindow = 200_000
	largeContextWindow   = 1_000_000
)

// largeContextModels get the 1M window. Matched by HasPrefix so dated suffixes
// like "claude-opus-4-7-20260201" still match.
var largeContextModels = []string{
	"claude-opus-4-6",
	"claude-opus-4-7",
	"claude-opus-4-8",
	"claude-sonnet-4-6",
}

// ContextWindow returns the model's context window size in tokens. Unknown
// or empty models fall back to the default 200k window.
func ContextWindow(model string) int {
	for _, prefix := range largeContextModels {
		if strings.HasPrefix(model, prefix) {
			return largeContextWindow
		}
	}
	return defaultContextWindow
}

// ContextDelta describes how the context window evolved over a range of
// inference cycles. Percentage fields are 0..100. "Context tokens" means
// input + cache_read + cache_creation: under prompt caching, input_tokens alone
// is only the new non-cached portion.
type ContextDelta struct {
	FirstInputTokens int // first cycle's context-window snapshot
	LastInputTokens  int // last cycle's context-window snapshot
	DeltaTokens      int // max(0, Last - First); never negative
	WindowSize       int // 200_000 or 1_000_000
	FirstUsagePct    float64
	LastUsagePct     float64
}

// contextSnapshot returns the full window size from a usage record:
// input_tokens + cache_read + cache_creation. Excludes output tokens, which
// aren't part of the window the next call sees.
func contextSnapshot(u Usage) int {
	return u.InputTokens + u.CacheReadTokens + u.CacheCreationTokens
}

// ComputeContextDelta returns first/last context snapshots across cycles as a
// delta and window percentages, or nil if no cycle has a non-zero snapshot.
// Window size comes from the first non-zero cycle (the anchor for rare mixed-model turns).
func ComputeContextDelta(cycles []InferenceCycle) *ContextDelta {
	first, last := -1, -1
	for i, c := range cycles {
		if contextSnapshot(c.Usage) > 0 {
			if first == -1 {
				first = i
			}
			last = i
		}
	}
	if first == -1 {
		return nil
	}

	window := ContextWindow(cycles[first].Model)
	fIn := contextSnapshot(cycles[first].Usage)
	lIn := contextSnapshot(cycles[last].Usage)

	delta := max(lIn-fIn, 0)

	return &ContextDelta{
		FirstInputTokens: fIn,
		LastInputTokens:  lIn,
		DeltaTokens:      delta,
		WindowSize:       window,
		FirstUsagePct:    windowPct(fIn, window),
		LastUsagePct:     windowPct(lIn, window),
	}
}

// ContextDelta returns the per-chunk context-window evolution. Returns nil
// when the chunk isn't an AI chunk or has no cycles with token data.
func (c *Chunk) ContextDelta() *ContextDelta {
	if c.Type != AIChunk {
		return nil
	}
	return ComputeContextDelta(c.Cycles)
}

// ContextUsagePct returns a token snapshot as a percentage of the model's
// context window. ok is false when inputTokens <= 0. Low-level helper: callers
// holding a token count + model should use this rather than rebuild a []Chunk.
func ContextUsagePct(inputTokens int, model string) (pct float64, window int, ok bool) {
	if inputTokens <= 0 {
		return 0, 0, false
	}
	w := ContextWindow(model)
	return windowPct(inputTokens, w), w, true
}

// SessionContextUsage returns the last non-zero context snapshot across a
// session's AI chunks as a window percentage. ok is false when no chunk reports
// usage. For callers holding a full []Chunk; lighter ones should use ContextUsagePct.
func SessionContextUsage(chunks []Chunk) (pct float64, window int, ok bool) {
	for i := len(chunks) - 1; i >= 0; i-- {
		c := chunks[i]
		if c.Type != AIChunk {
			continue
		}
		if snap := contextSnapshot(c.Usage); snap > 0 {
			return ContextUsagePct(snap, c.Model)
		}
	}
	return 0, 0, false
}

func windowPct(n, window int) float64 {
	if window <= 0 {
		return 0
	}
	return float64(n) * 100.0 / float64(window)
}
