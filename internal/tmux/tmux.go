// Package tmux is a thin wrapper around the tmux CLI for discovering, reading,
// controlling, and managing panes.
//
// Design notes (see docs/superpowers/specs/2026-06-12-argus-design.md):
//   - Always request explicit -F formats; never screen-scrape default output.
//   - Key state on pane_id (%N), stable and never reused — never on
//     session:window.pane indices, which renumber.
package tmux

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/MunifTanjim/argus/internal/shell"
)

// fieldSep (ASCII unit separator) delimits -F format fields so values containing
// spaces/tabs (e.g. paths) parse unambiguously.
const fieldSep = "\x1f"

// Client drives a single tmux server. A zero socket targets the user's default
// server; a non-empty socket targets a private "tmux -L <socket>" server that
// argus owns and starts config-less (see args).
type Client struct {
	bin    string
	socket string
}

// New returns a Client for the given socket name. Pass "" for the default server.
func New(socket string) *Client {
	return &Client{bin: "tmux", socket: socket}
}

// args prepends tmux's global flags (socket selection) to a subcommand. Private
// (argus-owned) sockets also start config-less ("-f /dev/null") so the user's
// ~/.tmux.conf can't leak in; -f is a no-op once the server is running. The
// default server is never given -f — argus must not alter the user's own tmux.
func (c *Client) args(sub ...string) []string {
	var a []string
	if c.socket != "" {
		a = append(a, "-L", c.socket, "-f", "/dev/null")
	}
	return append(a, sub...)
}

// run executes a tmux subcommand and returns its stdout. Errors include stderr.
func (c *Client) run(ctx context.Context, sub ...string) (string, error) {
	cmd := shell.NewCommandContext(ctx, c.bin, c.args(sub...)...)
	if err := cmd.Run(); err != nil {
		return cmd.StdOut().String(), &Error{Args: sub, Stderr: cmd.StdErr().TrimSpace().String(), Err: err}
	}
	return cmd.StdOut().String(), nil
}

// Error wraps a failed tmux invocation.
type Error struct {
	Args   []string
	Stderr string
	Err    error
}

func (e *Error) Error() string {
	if e.Stderr != "" {
		return fmt.Sprintf("tmux %s: %s", strings.Join(e.Args, " "), e.Stderr)
	}
	return fmt.Sprintf("tmux %s: %v", strings.Join(e.Args, " "), e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// noServer reports whether an error means the tmux server simply isn't running,
// which argus treats as "no sessions" rather than a failure.
func noServer(err error) bool {
	var te *Error
	if !errors.As(err, &te) {
		return false
	}
	s := te.Stderr
	return strings.Contains(s, "no server running") ||
		strings.Contains(s, "error connecting") ||
		strings.Contains(s, "no current session")
}

// SocketBaseFromEnv returns the tmux socket basename from a $TMUX value (format
// "<socket-path>,<pid>,<session>"), or "" when not inside tmux. The basename
// identifies the server (e.g. "default", "argus").
func SocketBaseFromEnv(tmuxEnv string) string {
	if tmuxEnv == "" {
		return ""
	}
	sockPath := tmuxEnv
	if i := strings.IndexByte(tmuxEnv, ','); i >= 0 {
		sockPath = tmuxEnv[:i]
	}
	return filepath.Base(sockPath)
}

// Reveal brings the calling tmux client to the given pane in one step. Runs as a
// subprocess inheriting $TMUX, so it targets the caller's own client; use the
// default-server client since switch-client cannot cross tmux servers.
func (c *Client) Reveal(ctx context.Context, paneID string) error {
	_, err := c.run(ctx, "switch-client", "-t", paneID)
	return err
}

// Version returns the tmux version string (e.g. "tmux next-3.7").
func (c *Client) Version(ctx context.Context) (string, error) {
	out, err := c.run(ctx, "-V")
	return strings.TrimSpace(out), err
}

// Available reports whether the tmux binary is present and runnable. `tmux -V` is
// a cheap probe: it prints the version without starting a server.
func (c *Client) Available(ctx context.Context) bool {
	_, err := c.Version(ctx)
	return err == nil
}

// Pane is one tmux pane and the process running in it.
type Pane struct {
	PaneID         string // stable "%N" identifier
	SessionName    string
	WindowIndex    int
	PaneIndex      int
	PanePID        int
	CurrentCommand string // foreground process name (may be disguised, e.g. a version string)
	CurrentPath    string
	TTY            string // pane tty, e.g. "/dev/ttys002"
	Active         bool
	Dead           bool
	InMode         bool // in copy/choose mode; input is intercepted
}

// paneFormat lists the fields requested, in order, joined by fieldSep.
var paneFormat = strings.Join([]string{
	"#{pane_id}",
	"#{session_name}",
	"#{window_index}",
	"#{pane_index}",
	"#{pane_pid}",
	"#{pane_current_command}",
	"#{pane_current_path}",
	"#{pane_tty}",
	"#{pane_active}",
	"#{pane_dead}",
	"#{pane_in_mode}",
}, fieldSep)

// ListPanes returns every pane on the server. If no server is running it returns
// an empty slice and no error.
func (c *Client) ListPanes(ctx context.Context) ([]Pane, error) {
	out, err := c.run(ctx, "list-panes", "-a", "-F", paneFormat)
	if err != nil {
		if noServer(err) {
			return nil, nil
		}
		return nil, err
	}
	var panes []Pane
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		p, perr := parsePane(line)
		if perr != nil {
			return nil, perr
		}
		panes = append(panes, p)
	}
	return panes, nil
}

func parsePane(line string) (Pane, error) {
	// tmux >=3.4 vis-escapes the 0x1F separator as literal "\037"; older tmux emits
	// the raw byte. Normalize before splitting.
	line = strings.ReplaceAll(line, `\037`, fieldSep)
	f := strings.Split(line, fieldSep)
	if len(f) != 11 {
		return Pane{}, fmt.Errorf("tmux: unexpected pane format (%d fields): %q", len(f), line)
	}
	return Pane{
		PaneID:         f[0],
		SessionName:    f[1],
		WindowIndex:    atoi(f[2]),
		PaneIndex:      atoi(f[3]),
		PanePID:        atoi(f[4]),
		CurrentCommand: f[5],
		CurrentPath:    f[6],
		TTY:            f[7],
		Active:         f[8] == "1",
		Dead:           f[9] == "1",
		InMode:         f[10] == "1",
	}, nil
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// focusFormat queries the three facts that together mean a client is showing a pane:
// active pane of the active window of an attached session.
var focusFormat = strings.Join([]string{
	"#{pane_id}",
	"#{pane_active}",
	"#{window_active}",
	"#{session_attached}",
}, fieldSep)

// IsFocused reports whether an attached client is currently displaying paneID.
// Used to suppress desktop notifications for a session the user is already viewing.
// An empty paneID, or no server running, is never focused.
func (c *Client) IsFocused(ctx context.Context, paneID string) (bool, error) {
	if paneID == "" {
		return false, nil
	}
	out, err := c.run(ctx, "list-panes", "-a", "-F", focusFormat)
	if err != nil {
		if noServer(err) {
			return false, nil
		}
		return false, err
	}
	return paneFocused(out, paneID)
}

// paneFocused scans focusFormat output for paneID and reports whether it is the
// active pane of the active window of an attached session.
func paneFocused(out, paneID string) (bool, error) {
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if line == "" {
			continue
		}
		// Normalize tmux's vis-escaped separator before splitting (see parsePane).
		line = strings.ReplaceAll(line, `\037`, fieldSep)
		f := strings.Split(line, fieldSep)
		if len(f) != 4 {
			return false, fmt.Errorf("tmux: unexpected focus format (%d fields): %q", len(f), line)
		}
		if f[0] == paneID {
			return f[1] == "1" && f[2] == "1" && atoi(f[3]) > 0, nil
		}
	}
	return false, nil
}

// CaptureOpts controls capture-pane behavior.
type CaptureOpts struct {
	// Escapes includes color/attribute escape sequences (-e).
	Escapes bool
	// FullScrollback captures from the start of history (-S -), not just the
	// visible area.
	FullScrollback bool
	// NoJoin omits -J, so each output line is a physical pane row (wrapped lines
	// stay wrapped) — matches exactly what the pane shows.
	NoJoin bool
}

// CapturePane returns the rendered text of a pane. Wrapped lines are joined and
// trailing spaces preserved (-J) unless NoJoin is set.
func (c *Client) CapturePane(ctx context.Context, paneID string, opts CaptureOpts) (string, error) {
	sub := []string{"capture-pane", "-p", "-t", paneID}
	if !opts.NoJoin {
		sub = append(sub, "-J")
	}
	if opts.Escapes {
		sub = append(sub, "-e")
	}
	if opts.FullScrollback {
		sub = append(sub, "-S", "-")
	}
	return c.run(ctx, sub...)
}

// PaneInMode reports whether a pane is in copy/view/choose mode, where tmux
// intercepts keystrokes instead of passing them to the running program.
func (c *Client) PaneInMode(ctx context.Context, paneID string) (bool, error) {
	out, err := c.run(ctx, "display-message", "-p", "-t", paneID, "-F", "#{pane_in_mode}")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "1", nil
}

// CancelMode exits a pane's copy/view mode (-X cancel), returning it to normal
// input. tmux errors if the pane is not in a mode, so gate calls on PaneInMode.
func (c *Client) CancelMode(ctx context.Context, paneID string) error {
	_, err := c.run(ctx, "send-keys", "-t", paneID, "-X", "cancel")
	return err
}

// SendText sends literal text to a pane (-l). Does NOT submit; send "Enter" via SendKeys.
func (c *Client) SendText(ctx context.Context, paneID, text string) error {
	_, err := c.run(ctx, "send-keys", "-t", paneID, "-l", "--", text)
	return err
}

// bracketedPaste wraps text as a terminal bracketed paste and normalizes line
// endings to bare CR. This is the only way injected newlines survive: outside a
// paste a raw LF is dropped and a raw CR submits the line. (Verified vs Claude Code.)
func bracketedPaste(text string) string {
	body := strings.NewReplacer("\r\n", "\r", "\n", "\r").Replace(text)
	return "\x1b[200~" + body + "\x1b[201~"
}

// PasteText injects text as a bracketed paste so embedded newlines survive (see
// bracketedPaste). Does NOT submit. Prefer for multi-line input; use SendText for
// single-line so interactive triggers (slash menus, @-mentions) still fire as typed.
func (c *Client) PasteText(ctx context.Context, paneID, text string) error {
	_, err := c.run(ctx, "send-keys", "-t", paneID, "-l", "--", bracketedPaste(text))
	return err
}

// SendKeys sends one or more key names (e.g. "Enter", "Escape", "C-c") to a pane.
func (c *Client) SendKeys(ctx context.Context, paneID string, keys ...string) error {
	sub := append([]string{"send-keys", "-t", paneID, "--"}, keys...)
	_, err := c.run(ctx, sub...)
	return err
}

// NewSessionOpts configures a detached session.
type NewSessionOpts struct {
	Name    string   // session name; required
	Command string   // optional command to run (empty = default shell)
	Args    []string // optional arguments passed to Command as separate argv; ignored when Command is empty
	Cwd     string   // optional working directory for the session
	Width   int      // optional geometry; defaults to a TUI-friendly 120x40
	Height  int
}

// NewSession creates a detached session and returns the pane id of its first pane.
func (c *Client) NewSession(ctx context.Context, opts NewSessionOpts) (string, error) {
	out, err := c.run(ctx, newSessionArgs(opts)...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// newSessionArgs builds the `tmux new-session` argument list. Command/Args are
// appended as separate trailing args so tmux execs them directly (no shell),
// preserving spaces and newlines.
func newSessionArgs(opts NewSessionOpts) []string {
	w, h := opts.Width, opts.Height
	if w == 0 {
		w = 120
	}
	if h == 0 {
		h = 40
	}
	sub := []string{
		"new-session", "-d",
		"-s", opts.Name,
		"-x", strconv.Itoa(w),
		"-y", strconv.Itoa(h),
		"-P", "-F", "#{pane_id}",
	}
	if opts.Cwd != "" {
		sub = append(sub, "-c", opts.Cwd)
	}
	if opts.Command != "" {
		sub = append(sub, opts.Command)
		sub = append(sub, opts.Args...)
	}
	return sub
}

// SetOption sets a tmux option on target (e.g. a session name):
// `set-option -t <target> <option> <value>`.
func (c *Client) SetOption(ctx context.Context, target, option, value string) error {
	_, err := c.run(ctx, "set-option", "-t", target, option, value)
	return err
}

// attachArgs builds the full argv (argv[0] included) for exec'ing into an
// attached tmux client on this Client's server. Split out for testability.
func (c *Client) attachArgs(bin, name string) []string {
	return append([]string{bin}, c.args("attach-session", "-t", name)...)
}

// Attach hands the current terminal to tmux by replacing this process with
// `tmux [-L socket] attach-session -t <name>` via syscall.Exec. tmux then owns
// the real TTY — correct resize and signal handling — and the process exits with
// tmux when the session ends. Does NOT return on success; use only from a CLI
// foreground command, never from the node.
func (c *Client) Attach(name string) error {
	bin, err := exec.LookPath(c.bin)
	if err != nil {
		return err
	}
	return syscall.Exec(bin, c.attachArgs(bin, name), os.Environ())
}

// KillPane kills a single pane. If it is the last pane in its session, the
// session ends too.
func (c *Client) KillPane(ctx context.Context, paneID string) error {
	_, err := c.run(ctx, "kill-pane", "-t", paneID)
	return err
}

// KillSession kills a session by name.
func (c *Client) KillSession(ctx context.Context, name string) error {
	_, err := c.run(ctx, "kill-session", "-t", name)
	return err
}

// KillServer terminates the entire tmux server for this client's socket. Used
// mainly to tear down argus's private socket (and in tests).
func (c *Client) KillServer(ctx context.Context) error {
	_, err := c.run(ctx, "kill-server")
	if err != nil && noServer(err) {
		return nil
	}
	return err
}
