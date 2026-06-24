// Package tmux is a thin wrapper around the tmux CLI (driven via os/exec) for
// discovering, reading, controlling, and managing panes. argus uses it to
// observe Claude Code sessions on the user's default tmux server and to spawn
// its own sessions on a private socket.
//
// Design notes (see docs/superpowers/specs/2026-06-12-argus-design.md):
//   - Always request explicit -F formats; never screen-scrape default output.
//   - Key state on pane_id (%N), which is stable and never reused — never on
//     session:window.pane indices, which renumber.
package tmux

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/MunifTanjim/argus/internal/shell"
)

// fieldSep is an unlikely separator (ASCII unit separator) used inside -F
// formats so values containing spaces/tabs (e.g. paths) parse unambiguously.
const fieldSep = "\x1f"

// Client drives a single tmux server. A zero socket targets the user's default
// server; a non-empty socket targets a private "tmux -L <socket>" server.
type Client struct {
	bin    string
	socket string
}

// New returns a Client for the given socket name. Pass "" for the default server.
func New(socket string) *Client {
	return &Client{bin: "tmux", socket: socket}
}

// args prepends the binary's global flags (socket selection) to a subcommand.
func (c *Client) args(sub ...string) []string {
	var a []string
	if c.socket != "" {
		a = append(a, "-L", c.socket)
	}
	return append(a, sub...)
}

// run executes a tmux subcommand and returns its stdout. The returned error
// includes stderr for diagnosis.
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

// SocketBaseFromEnv returns the tmux socket basename from a $TMUX value, whose
// format is "<socket-path>,<pid>,<session>". It returns "" when not inside tmux.
// The basename identifies the server: "default" for the user's normal server,
// "argus" for argus's private socket.
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

// Reveal brings the calling tmux client to the given pane, changing its session,
// window, and pane in one step: switch-client treats a target containing ':',
// '.' or '%' (a pane id is "%N") as that special case. It runs as a subprocess
// that inherits $TMUX, so it targets the caller's own client. Use the
// default-server client; switch-client cannot cross tmux servers.
func (c *Client) Reveal(ctx context.Context, paneID string) error {
	_, err := c.run(ctx, "switch-client", "-t", paneID)
	return err
}

// Version returns the tmux version string (e.g. "tmux next-3.7").
func (c *Client) Version(ctx context.Context) (string, error) {
	out, err := c.run(ctx, "-V")
	return strings.TrimSpace(out), err
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

// CaptureOpts controls capture-pane behavior.
type CaptureOpts struct {
	// Escapes includes color/attribute escape sequences (-e).
	Escapes bool
	// FullScrollback captures from the start of history (-S -), not just the
	// visible area.
	FullScrollback bool
}

// CapturePane returns the rendered text of a pane. Wrapped lines are joined and
// trailing spaces preserved (-J) for stable parsing.
func (c *Client) CapturePane(ctx context.Context, paneID string, opts CaptureOpts) (string, error) {
	sub := []string{"capture-pane", "-p", "-J", "-t", paneID}
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

// SendText sends literal text to a pane without interpreting key names (-l). It
// does NOT submit; send "Enter" via SendKeys afterwards.
func (c *Client) SendText(ctx context.Context, paneID, text string) error {
	_, err := c.run(ctx, "send-keys", "-t", paneID, "-l", "--", text)
	return err
}

// bracketedPaste wraps text as a terminal bracketed paste (ESC[200~ … ESC[201~)
// and normalizes every line ending to a bare CR. A TUI in bracketed-paste mode
// inserts the body verbatim, so each CR lands as a literal newline in its input.
// This is the only way injected newlines survive: sent outside a paste, a raw LF
// is dropped and a raw CR submits the line. (Verified against Claude Code.)
func bracketedPaste(text string) string {
	body := strings.NewReplacer("\r\n", "\r", "\n", "\r").Replace(text)
	return "\x1b[200~" + body + "\x1b[201~"
}

// PasteText injects text as a bracketed paste so embedded newlines are preserved
// as line breaks (see bracketedPaste). Like SendText it does NOT submit; send
// "Enter" via SendKeys afterwards. Prefer it for multi-line input; single-line
// input can use SendText so interactive triggers (slash menus, @-mentions) still
// fire as if typed.
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
	Name    string // session name; required
	Command string // optional command to run (empty = default shell)
	Cwd     string // optional working directory for the session
	Width   int    // optional geometry; defaults to a TUI-friendly 120x40
	Height  int
}

// NewSession creates a detached session and returns the pane id of its first pane.
func (c *Client) NewSession(ctx context.Context, opts NewSessionOpts) (string, error) {
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
	}
	out, err := c.run(ctx, sub...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
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
