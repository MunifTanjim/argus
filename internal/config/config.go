package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"

	"github.com/MunifTanjim/argus/internal/pagent/routine"
)

// Config is the resolved argus configuration. Precedence: flags > env > file > defaults.
type Config struct {
	Socket string
	Bin    string // argus binary that agent hooks invoke
	Token  string // shared gateway token: presented by clients/nodes, required by the gateway
	// Mode selects the run topology: "node", "gateway", or empty to infer from URL/token.
	Mode      string
	Gateway   GatewayConfig
	Node      NodeConfig
	Push      PushConfig
	Log       LogConfig
	Tunnel    TunnelConfig
	Tmux      TmuxConfig
	E2EE      E2EEConfig
	Lock      LockConfig
	Workspace WorkspaceConfig
	TUI       TUIConfig
	MCP       MCPConfig
	Agents    AgentsConfig
	// LuaPath is the init.lua to load; empty when config is off.
	LuaPath string
}

// WorkspaceConfig tunes workspace (git worktree) management.
type WorkspaceConfig struct {
	// WorktreeDirTemplate is the Go-template path for a new worktree, resolved
	// relative to the project's main working tree. Vars: {{.Repo.Name}},
	// {{.Branch.Name}}, {{.Branch.Slug}}.
	WorktreeDirTemplate string
	// IssueBranchTemplate names the branch of a workspace created from an
	// issue. Vars: {{.Issue.Number}}, {{.Issue.Title}}, {{.Issue.Slug}}.
	IssueBranchTemplate string
	// AutoAdoptDirs bounds automatic adoption: a session or worktree outside
	// every listed directory is not recorded. A leading "~" is the home
	// directory; an empty list turns automatic adoption off.
	AutoAdoptDirs []string
}

type TUIConfig struct {
	// KeyTimeout is how long a key that also starts a longer mapping waits for
	// the next key.
	KeyTimeout time.Duration
	// LeaderKey is the key that `<Leader>` stands for in keymaps.
	LeaderKey string
	// Mouse captures the mouse on every screen when on. When off, the terminal
	// keeps text selection, and only the live screen takes the wheel.
	Mouse MouseMode
	// VerboseTranscript shows runs of thinking and tool calls expanded by
	// default instead of folded into a one-line summary.
	VerboseTranscript bool
	// Keymaps is tui.keymap as written: screen → key sequence → mapping.
	Keymaps map[string]map[string]Mapping
}

type MouseMode string

const (
	MouseOff MouseMode = "off"
	MouseOn  MouseMode = "on"
)

type GatewayConfig struct {
	URL        string
	ListenAddr string
}

type NodeConfig struct {
	ID    string
	Label string
}

type PushConfig struct {
	Desktop DesktopConfig
	Mobile  MobileConfig
}

type DesktopConfig struct {
	Enabled bool // render native desktop notifications on this node (opt-in)
}

// MobileConfig tunes device (mobile) push delivery.
type MobileConfig struct {
	// Delay is the grace period before a mobile push fires. 0 (default) or any
	// non-positive value fires immediately. Re-checked when it elapses: sent
	// only if the session is still awaiting input or idle.
	Delay time.Duration
}

type LogConfig struct {
	Level  string
	Format string
}

type TunnelConfig struct {
	Provider   string
	Cloudflare CloudflareConfig
	External   ExternalConfig
	Zrok       ZrokConfig
	Ngrok      NgrokConfig
}

type CloudflareConfig struct {
	Token      string
	TunnelName string
	Hostname   string
}

type ExternalConfig struct {
	URL string // gateway's public URL when the tunnel is managed outside argus
}

type ZrokConfig struct {
	Name string // reserved name selection ("namespace:name" or "name") for a stable URL
}

type NgrokConfig struct {
	Domain string // reserved/custom domain ("" => the account's static dev domain)
}

// TmuxConfig names the tmux mirror sessions spawned for terminal.* calls. The
// prefix and suffix wrap the "argus-mirror-<termID>" marker so a reaper can
// match them without touching unrelated sessions.
type TmuxConfig struct {
	MirrorSessionPrefix string
	MirrorSessionSuffix string
}

// E2EE enables the blind-relay end-to-end-encrypted transport. Off by default; must be set on gateway, node, and client to opt the fleet in.
type E2EEConfig struct {
	Enabled bool
}

// MCPConfig configures the node's local MCP server. Port 0 disables it.
type MCPConfig struct {
	Port int
}

// Lock.Genesis is the base64 trust-log genesis this install is pinned to; "" = not configured (open/TOFU).
type LockConfig struct {
	Genesis string
}

// AgentsConfig configures persistent agents. Empty directory settings resolve
// to the argus data directory.
type AgentsConfig struct {
	DefaultDir string
	ProfileDir string
	SkillsDir  string
	Primary    string
	Defaults   AgentDefaults
}

// AgentDefaults are the settings an agent inherits when its own definition
// leaves them unset.
type AgentDefaults struct {
	Agent                 string
	Model                 string
	Timezone              string
	IdleClose             time.Duration
	MaxConcurrentSessions int
	Git                   struct {
		Mode        string
		CommitFiles bool
	}
	Memory struct {
		Mode string
		// CoreBudget is the size limit in bytes; negative when the configured
		// value does not parse.
		CoreBudget   int64
		CaptureAfter time.Duration
		TidyAt       string
		LogEntries   int
		Extractor    string
	}
	Proactivity string
	QuietHours  string
	Notify      string
	Brief       struct {
		Enabled bool
		At      string
	}
	Limits struct {
		MinInterval        time.Duration
		MaxEnabledRoutines int
		MaxCheckBacks      int
		RunTimeout         time.Duration
		MaxRunsPerHour     int
		// CatchUpWindow and PauseAfterAway are negative when the configured
		// value does not parse.
		CatchUpWindow  time.Duration
		PauseAfterAway time.Duration
	}
}

// ParseSize parses a byte size such as "24KB", "2MB" or "512" (1024-based).
func ParseSize(s string) (int64, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	mult := int64(1)
	for _, u := range []struct {
		suffix string
		mult   int64
	}{{"KB", 1 << 10}, {"MB", 1 << 20}, {"B", 1}} {
		if strings.HasSuffix(s, u.suffix) {
			s, mult = strings.TrimSpace(strings.TrimSuffix(s, u.suffix)), u.mult
			break
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid size")
	}
	return n * mult, nil
}

func resolveDir(v, name string) string {
	if v == "" {
		return GetDataPath(name)
	}
	if v == "~" || strings.HasPrefix(v, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(v, "~"))
		}
	}
	return v
}

func DefaultSocket() string { return GetRuntimePath("argus.sock") }

// defaults are the built-in fallback values for unset keys.
var defaults = map[string]any{
	"socket":                          DefaultSocket(),
	"bin":                             "argus",
	"token":                           "",
	"gateway.url":                     "",
	"gateway.listen-addr":             ":8443",
	"mode":                            "",
	"node.id":                         "",
	"node.label":                      "",
	"push.desktop.enabled":            false,
	"push.mobile.delay":               "0s",
	"log.level":                       "info",
	"log.format":                      "pretty",
	"tunnel.provider":                 "",
	"tunnel.cloudflare.token":         "",
	"tunnel.cloudflare.tunnel-name":   "",
	"tunnel.cloudflare.hostname":      "",
	"tunnel.external.url":             "",
	"tunnel.zrok.name":                "",
	"tunnel.ngrok.domain":             "",
	"tmux.mirror-session-prefix":      "_",
	"tmux.mirror-session-suffix":      "_",
	"e2ee.enabled":                    false,
	"lock.genesis":                    "",
	"workspace.worktree-dir-template": ".worktrees/{{.Branch.Slug}}",
	"workspace.issue-branch-template": "issue-{{.Issue.Number}}-{{.Issue.Slug}}",
	"workspace.auto-adopt-dirs":       []string{"~"},
	"tui.key-timeout":                 "1s",
	"tui.leader-key":                  "<Space>",
	"tui.mouse":                       "on",
	"tui.verbose-transcript":          false,
	"mcp.port":                        7734,

	"agents.default-dir":                          "",
	"agents.profile-dir":                          "",
	"agents.skills-dir":                           "",
	"agents.primary":                              "",
	"agents.defaults.agent":                       "claude",
	"agents.defaults.model":                       "",
	"agents.defaults.timezone":                    "auto",
	"agents.defaults.idle-close":                  "30m",
	"agents.defaults.max-concurrent-sessions":     2,
	"agents.defaults.git.mode":                    "nested",
	"agents.defaults.git.commit-files":            false,
	"agents.defaults.memory.mode":                 "auto",
	"agents.defaults.memory.core-budget":          "24KB",
	"agents.defaults.memory.capture-after":        "2m",
	"agents.defaults.memory.tidy-at":              "03:00",
	"agents.defaults.memory.log-entries":          20,
	"agents.defaults.memory.extractor":            "",
	"agents.defaults.proactivity":                 "default",
	"agents.defaults.quiet-hours":                 "",
	"agents.defaults.brief.enabled":               false,
	"agents.defaults.brief.at":                    "08:00",
	"agents.defaults.notify":                      "needs-you",
	"agents.defaults.limits.min-interval":         "15m",
	"agents.defaults.limits.max-enabled-routines": 20,
	"agents.defaults.limits.max-check-backs":      10,
	"agents.defaults.limits.run-timeout":          "1h",
	"agents.defaults.limits.max-runs-per-hour":    12,
	"agents.defaults.limits.catch-up-window":      "12h",
	"agents.defaults.limits.pause-after-away":     "7d",
}

// Load configures v with argus's defaults, env binding, and config file. configPath,
// when non-empty, is read instead of the default ConfigDir/config.yaml. skipFile skips
// reading any config file, leaving only defaults and env.
// A missing default file is not an error; a missing explicit configPath is.
func Load(v *viper.Viper, configPath string, skipFile bool) error {
	for key, val := range defaults {
		v.SetDefault(key, val)
	}

	v.SetEnvPrefix("ARGUS")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	for key := range defaults {
		// Workspace settings come only from the config file.
		if !strings.HasPrefix(key, "workspace.") {
			_ = v.BindEnv(key)
		}
	}
	// Preserve historical env var names where the derived ARGUS_<KEY_PATH> would differ.
	_ = v.BindEnv("tunnel.cloudflare.token", "ARGUS_CLOUDFLARE_TOKEN")
	_ = v.BindEnv("tunnel.cloudflare.tunnel-name", "ARGUS_CLOUDFLARE_TUNNEL_NAME")
	_ = v.BindEnv("tunnel.cloudflare.hostname", "ARGUS_CLOUDFLARE_HOSTNAME")
	_ = v.BindEnv("tunnel.external.url", "ARGUS_EXTERNAL_URL")
	_ = v.BindEnv("tunnel.zrok.name", "ARGUS_ZROK_NAME")
	_ = v.BindEnv("tunnel.ngrok.domain", "ARGUS_NGROK_DOMAIN")

	if skipFile {
		return nil
	}

	v.SetConfigType("yaml")
	if configPath != "" {
		v.SetConfigFile(configPath)
	} else {
		v.SetConfigName("config")
		v.AddConfigPath(ConfigDir)
	}

	if err := v.ReadInConfig(); err != nil {
		if configPath != "" {
			return fmt.Errorf("read config %s: %w", configPath, err)
		}
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) {
			return fmt.Errorf("read config: %w", err)
		}
	}
	return nil
}

// Validate checks resolved values for constraints that would otherwise fail
// opaquely at runtime. Returns the first problem found.
func (c Config) Validate() error {
	// tmux session names cannot contain ':' or '.' (target-spec separators), so a
	// mirror-session affix carrying either would silently break mirror creation.
	for _, a := range []struct{ name, val string }{
		{"tmux.mirror-session-prefix", c.Tmux.MirrorSessionPrefix},
		{"tmux.mirror-session-suffix", c.Tmux.MirrorSessionSuffix},
	} {
		if strings.ContainsAny(a.val, ":.") {
			return fmt.Errorf("%s %q must not contain ':' or '.' (tmux session name constraint)", a.name, a.val)
		}
	}
	if m := c.TUI.Mouse; m != MouseOn && m != MouseOff {
		return fmt.Errorf("tui.mouse %q must be %q or %q", m, MouseOn, MouseOff)
	}
	if c.MCP.Port < 0 || c.MCP.Port > 65535 {
		return fmt.Errorf("mcp.port %d must be between 0 and 65535", c.MCP.Port)
	}
	d := c.Agents.Defaults
	for _, e := range []struct {
		key, val string
		allowed  []string
	}{
		{"agents.defaults.git.mode", d.Git.Mode, []string{"nested", "outer"}},
		{"agents.defaults.memory.mode", d.Memory.Mode, []string{"auto", "review", "off"}},
		{"agents.defaults.notify", d.Notify, []string{"all", "needs-you", "off"}},
		{"agents.defaults.proactivity", d.Proactivity, []string{"off", "low", "default", "high"}},
	} {
		if e.val != "" && !slices.Contains(e.allowed, e.val) {
			return fmt.Errorf("%s %q must be one of %s", e.key, e.val, strings.Join(e.allowed, ", "))
		}
	}
	if d.Memory.CoreBudget < 0 {
		return errors.New("agents.defaults.memory.core-budget must be a size such as 24KB")
	}
	if d.Limits.CatchUpWindow < 0 {
		return errors.New("agents.defaults.limits.catch-up-window must be a duration such as 12h")
	}
	if d.Limits.PauseAfterAway < 0 {
		return errors.New("agents.defaults.limits.pause-after-away must be a duration such as 7d")
	}
	return nil
}

// days reads a duration that may use a day suffix ("7d"); -1 when it does not parse.
func days(v *viper.Viper, key string) time.Duration {
	d, err := routine.ParseDuration(strings.TrimSpace(v.GetString(key)))
	if err != nil || d < 0 {
		return -1
	}
	return d
}

// FromViper reads resolved values out of v into a Config. Explicit Get calls (not
// Unmarshal) keep bound-flag precedence predictable.
func FromViper(v *viper.Viper) Config {
	return Config{
		Socket: v.GetString("socket"),
		Bin:    v.GetString("bin"),
		Token:  v.GetString("token"),
		Mode:   v.GetString("mode"),
		Gateway: GatewayConfig{
			URL:        v.GetString("gateway.url"),
			ListenAddr: v.GetString("gateway.listen-addr"),
		},
		Node: NodeConfig{
			ID:    v.GetString("node.id"),
			Label: v.GetString("node.label"),
		},
		Push: PushConfig{
			Desktop: DesktopConfig{
				Enabled: v.GetBool("push.desktop.enabled"),
			},
			Mobile: MobileConfig{
				Delay: v.GetDuration("push.mobile.delay"),
			},
		},
		Log: LogConfig{
			Level:  v.GetString("log.level"),
			Format: v.GetString("log.format"),
		},
		Tunnel: TunnelConfig{
			Provider: v.GetString("tunnel.provider"),
			Cloudflare: CloudflareConfig{
				Token:      v.GetString("tunnel.cloudflare.token"),
				TunnelName: v.GetString("tunnel.cloudflare.tunnel-name"),
				Hostname:   v.GetString("tunnel.cloudflare.hostname"),
			},
			External: ExternalConfig{
				URL: v.GetString("tunnel.external.url"),
			},
			Zrok: ZrokConfig{
				Name: v.GetString("tunnel.zrok.name"),
			},
			Ngrok: NgrokConfig{
				Domain: v.GetString("tunnel.ngrok.domain"),
			},
		},
		Tmux: TmuxConfig{
			MirrorSessionPrefix: v.GetString("tmux.mirror-session-prefix"),
			MirrorSessionSuffix: v.GetString("tmux.mirror-session-suffix"),
		},
		E2EE: E2EEConfig{
			Enabled: v.GetBool("e2ee.enabled"),
		},
		Lock: LockConfig{
			Genesis: v.GetString("lock.genesis"),
		},
		Workspace: WorkspaceConfig{
			WorktreeDirTemplate: v.GetString("workspace.worktree-dir-template"),
			IssueBranchTemplate: v.GetString("workspace.issue-branch-template"),
			AutoAdoptDirs:       v.GetStringSlice("workspace.auto-adopt-dirs"),
		},
		TUI: TUIConfig{
			KeyTimeout:        v.GetDuration("tui.key-timeout"),
			LeaderKey:         v.GetString("tui.leader-key"),
			Mouse:             MouseMode(v.GetString("tui.mouse")),
			VerboseTranscript: v.GetBool("tui.verbose-transcript"),
		},
		MCP: MCPConfig{
			Port: v.GetInt("mcp.port"),
		},
		Agents: agentsFromViper(v),
	}
}

func agentsFromViper(v *viper.Viper) AgentsConfig {
	const p = "agents.defaults."
	a := AgentsConfig{
		DefaultDir: resolveDir(v.GetString("agents.default-dir"), "agents"),
		ProfileDir: resolveDir(v.GetString("agents.profile-dir"), "profile"),
		SkillsDir:  resolveDir(v.GetString("agents.skills-dir"), "skills"),
		Primary:    v.GetString("agents.primary"),
	}
	d := &a.Defaults
	d.Agent = v.GetString(p + "agent")
	d.Model = v.GetString(p + "model")
	d.Timezone = v.GetString(p + "timezone")
	d.IdleClose = v.GetDuration(p + "idle-close")
	d.MaxConcurrentSessions = v.GetInt(p + "max-concurrent-sessions")
	d.Git.Mode = v.GetString(p + "git.mode")
	d.Git.CommitFiles = v.GetBool(p + "git.commit-files")
	d.Memory.Mode = v.GetString(p + "memory.mode")
	if n, err := ParseSize(v.GetString(p + "memory.core-budget")); err == nil {
		d.Memory.CoreBudget = n
	} else {
		d.Memory.CoreBudget = -1
	}
	d.Memory.CaptureAfter = v.GetDuration(p + "memory.capture-after")
	d.Memory.TidyAt = v.GetString(p + "memory.tidy-at")
	d.Memory.LogEntries = v.GetInt(p + "memory.log-entries")
	d.Memory.Extractor = v.GetString(p + "memory.extractor")
	d.Proactivity = v.GetString(p + "proactivity")
	d.QuietHours = v.GetString(p + "quiet-hours")
	d.Notify = v.GetString(p + "notify")
	d.Brief.Enabled = v.GetBool(p + "brief.enabled")
	d.Brief.At = v.GetString(p + "brief.at")
	d.Limits.MinInterval = v.GetDuration(p + "limits.min-interval")
	d.Limits.MaxEnabledRoutines = v.GetInt(p + "limits.max-enabled-routines")
	d.Limits.MaxCheckBacks = v.GetInt(p + "limits.max-check-backs")
	d.Limits.RunTimeout = v.GetDuration(p + "limits.run-timeout")
	d.Limits.MaxRunsPerHour = v.GetInt(p + "limits.max-runs-per-hour")
	d.Limits.CatchUpWindow = days(v, p+"limits.catch-up-window")
	d.Limits.PauseAfterAway = days(v, p+"limits.pause-after-away")
	return a
}
