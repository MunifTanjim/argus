package push

import (
	"context"
	"errors"
	"log/slog"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

// errNotFound is the sentinel the lookPath seam returns when a tool is absent.
var errNotFound = errors.New("push: tool not found")

// clickCmd builds the argv to run when a notification is activated. nil disables
// click. Injected by the wiring layer so internal/push stays free of node/cmd
// dependencies — it only runs or embeds an opaque command.
type clickCmd func(sessionID string) []string

// OSNotifier is a Sink that renders a Notification as a native macOS desktop
// notification via alerter/Hammerspoon (clickable) or osascript (title+body).
// Best-effort: a missing tool, unsupported OS, or failure is logged and swallowed.
// macOS only for now.
type OSNotifier struct {
	log   *slog.Logger
	goos  string // runtime.GOOS; overridable in tests
	click clickCmd

	// seams (overridable in tests)
	run         func(ctx context.Context, name string, args ...string) error
	output      func(ctx context.Context, name string, args ...string) ([]byte, error)
	lookPath    func(name string) (string, error)
	hsAvailable func() bool
	iconPath    func() (string, bool) // materialized argus icon for --app-icon

	nameOnce sync.Once
	nameVal  string
}

// NewOSNotifier returns an OSNotifier for the current platform. log may be nil;
// click may be nil (title+body only).
func NewOSNotifier(log *slog.Logger, click clickCmd) *OSNotifier {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	o := &OSNotifier{
		log:   log,
		goos:  runtime.GOOS,
		click: click,
		run: func(ctx context.Context, name string, args ...string) error {
			return exec.CommandContext(ctx, name, args...).Run()
		},
		output: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).Output()
		},
		lookPath: func(name string) (string, error) {
			p, err := exec.LookPath(name)
			if err != nil {
				return "", errNotFound
			}
			return p, nil
		},
	}
	o.hsAvailable = o.detectHammerspoon
	o.iconPath = materializeIcon
	return o
}

// rendererName returns the selected renderer. The choice is static for the
// notifier's lifetime, so it's computed once and cached (computeRendererName scans
// PATH; Notify runs per notification).
func (o *OSNotifier) rendererName() string {
	o.nameOnce.Do(func() { o.nameVal = o.computeRendererName() })
	return o.nameVal
}

func (o *OSNotifier) computeRendererName() string {
	switch o.goos {
	case "darwin":
		if o.click != nil {
			// alerter is preferred: a self-contained binary that reports the click
			// on stdout and de-dupes via --group, with no hs.ipc bridge to set up.
			if _, err := o.lookPath("alerter"); err == nil {
				return "alerter"
			}
			if _, err := o.lookPath("hs"); err == nil && o.hsAvailable() {
				return "hammerspoon"
			}
		}
		return "osascript"
	default:
		return ""
	}
}

// detectHammerspoon reports whether the hs CLI can reach a running Hammerspoon
// with the ipc bridge loaded (trivial `hs -c`). When false, selection avoids a
// Hammerspoon renderer that would fail every call (e.g. exit 69 without hs.ipc).
func (o *OSNotifier) detectHammerspoon() bool {
	return o.run(context.Background(), "hs", "-c", "_=1") == nil
}

// Notify renders n on the local desktop. Best-effort; failures are logged only.
func (o *OSNotifier) Notify(ctx context.Context, n Notification) {
	// Detach from caller cancellation: alerter blocks until clicked, but callers
	// pass the peer conn ctx, which a reconnect would cancel mid-banner.
	ctx = context.WithoutCancel(ctx)

	// Brand the title (desktop banners carry no app identity, unlike mobile). n is
	// a value copy, so this doesn't affect the mobile dispatch sink.
	n.Title = "Argus · " + n.Title

	switch o.rendererName() {
	case "osascript":
		o.renderPlain(ctx, n)
	case "alerter":
		o.renderAlerter(ctx, n)
	case "hammerspoon":
		o.renderHammerspoon(ctx, n)
	default:
		o.log.Warn("desktop: unsupported OS for notifications", "goos", o.goos)
	}
}

// renderAlerter shows a clickable notification via `alerter`, which blocks until
// the user interacts and prints the result to stdout; on click it runs the click
// command. --group de-dupes per session natively. Runs in a goroutine so Notify
// stays non-blocking. Selection already verified alerter is on PATH and click != nil.
func (o *OSNotifier) renderAlerter(ctx context.Context, n Notification) {
	sessionID := n.SessionID()
	args := []string{"--title", n.Title, "--message", n.Body}
	if icon, ok := o.iconPath(); ok {
		args = append(args, "--app-icon", icon)
	}
	if sessionID != "" {
		args = append(args, "--group", sessionID)
	}
	go func() {
		out, err := o.output(ctx, "alerter", args...)
		if err != nil {
			// alerter failed — fall back to a non-clickable osascript banner.
			o.log.Warn("desktop: alerter failed, falling back to osascript", "err", err)
			o.renderPlain(ctx, n)
			return
		}
		switch strings.TrimSpace(string(out)) {
		case "@CONTENTCLICKED", "@ACTIONCLICKED":
			argv := o.click(sessionID)
			if len(argv) == 0 {
				return
			}
			if err := o.run(ctx, argv[0], argv[1:]...); err != nil {
				o.log.Warn("desktop: click command failed", "err", err)
			}
		}
	}()
}

// renderPlain is the dependency-free title+body path via osascript.
func (o *OSNotifier) renderPlain(ctx context.Context, n Notification) {
	name, args, ok := notifyArgv(o.goos, n)
	if !ok {
		o.log.Warn("desktop: unsupported OS for notifications", "goos", o.goos)
		return
	}
	if err := o.run(ctx, name, args...); err != nil {
		o.log.Warn("desktop: notify command failed", "cmd", name, "err", err)
	}
}

// renderHammerspoon shows the notification via a running Hammerspoon (hs -c),
// whose click callback runs the click command out-of-process. It does NOT raise
// the terminal window: there's no reliable way to identify the user's terminal
// app, so we'd risk activating the wrong one. Selection already verified hs is on
// PATH and click != nil.
func (o *OSNotifier) renderHammerspoon(ctx context.Context, n Notification) {
	sessionID := n.SessionID()
	argv := o.click(sessionID)
	notifyExpr := `hs.notify.new(function()` +
		` hs.execute(` + luaQuote(shellJoin(argv)) + `, true)` +
		` end, {title=` + luaQuote(n.Title) + `, informativeText=` + luaQuote(n.Body) + `, withdrawAfter=0})`
	// De-dupe per session: Lua globals persist across `hs -c` calls, so keep one
	// notification per session in _argus and withdraw the previous before sending.
	var lua string
	if sessionID == "" {
		lua = notifyExpr + `:send()`
	} else {
		k := luaQuote(sessionID)
		lua = `_argus = _argus or {};` +
			` local k = ` + k + `;` +
			` if _argus[k] then _argus[k]:withdraw() end;` +
			` local n = ` + notifyExpr + `;` +
			` _argus[k] = n; n:send()`
	}
	if err := o.run(ctx, "hs", "-c", lua); err != nil {
		// hs call failed — most often the hs.ipc bridge isn't loaded (exit 69).
		// Fall back to a non-clickable osascript banner.
		o.log.Warn("desktop: hammerspoon notify failed, falling back to osascript", "err", err)
		o.renderPlain(ctx, n)
	}
}

// luaQuote wraps s in Lua double quotes, escaping backslashes, double quotes,
// and control characters (\n, \r, \t) that are syntax errors in Lua short strings.
func luaQuote(s string) string {
	out := make([]rune, 0, len(s)+2)
	out = append(out, '"')
	for _, r := range s {
		switch r {
		case '\\':
			out = append(out, '\\', '\\')
		case '"':
			out = append(out, '\\', '"')
		case '\n':
			out = append(out, '\\', 'n')
		case '\r':
			out = append(out, '\\', 'r')
		case '\t':
			out = append(out, '\\', 't')
		default:
			out = append(out, r)
		}
	}
	out = append(out, '"')
	return string(out)
}

// shellJoin joins argv into a single shell command, single-quoting each arg so
// the click command survives hs.execute (which runs it via a shell).
func shellJoin(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return strings.Join(parts, " ")
}

// notifyArgv builds the osascript command and arguments to display (title, body).
// ok is false when the OS is unsupported (only macOS is supported).
func notifyArgv(goos string, n Notification) (name string, args []string, ok bool) {
	switch goos {
	case "darwin":
		script := `display notification ` + appleQuote(n.Body) +
			` with title ` + appleQuote(n.Title)
		return "osascript", []string{"-e", script}, true
	default:
		return "", nil, false
	}
}

// appleQuote wraps s in AppleScript double quotes, escaping embedded quotes and
// backslashes.
func appleQuote(s string) string {
	out := make([]rune, 0, len(s)+2)
	out = append(out, '"')
	for _, r := range s {
		if r == '"' || r == '\\' {
			out = append(out, '\\')
		}
		out = append(out, r)
	}
	out = append(out, '"')
	return string(out)
}
