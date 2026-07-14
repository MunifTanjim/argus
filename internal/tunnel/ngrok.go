package tunnel

import (
	"log/slog"
	"strings"
)

// Ngrok runs the ngrok agent as a public HTTP tunnel to the gateway. It needs an authtoken
// (NGROK_AUTHTOKEN or `ngrok config add-authtoken`), ensured in pre-flight by ensureNgrokAuth.
type Ngrok struct {
	Bin    string // path to the ngrok binary
	Domain string // reserved/custom domain via --url; "" => the account's static dev domain
}

func (n Ngrok) Name() string { return "ngrok" }

func (n Ngrok) Command(origin string) (CommandSpec, error) {
	// logfmt to stdout: non-interactive, machine-readable output.
	args := []string{"http", origin, "--log", "stdout", "--log-format", "logfmt"}
	if n.Domain != "" {
		args = append(args, "--url", n.Domain)
	}
	return CommandSpec{Path: n.Bin, Args: args}, nil
}

// ExtractURL returns the public URL from the logfmt "started tunnel" line, keying off its
// `url=` field, never the local `addr=`.
func (n Ngrok) ExtractURL(line string) (string, bool) {
	if !strings.Contains(line, "started tunnel") {
		return "", false
	}
	for _, f := range strings.Fields(line) {
		v, ok := strings.CutPrefix(f, "url=")
		if !ok {
			continue
		}
		v = strings.Trim(v, `"`)
		if strings.HasPrefix(v, "https://") || strings.HasPrefix(v, "http://") {
			return v, true
		}
	}
	return "", false
}

// ngrokLevelByToken maps ngrok's logfmt level tokens to slog levels; info is demoted to
// Debug as chatty steady-state noise. ngrok spells them "eror"/"crit".
var ngrokLevelByToken = map[string]slog.Level{
	"trace": slog.LevelDebug,
	"debug": slog.LevelDebug,
	"info":  slog.LevelDebug,
	"warn":  slog.LevelWarn,
	"eror":  slog.LevelError,
	"error": slog.LevelError,
	"crit":  slog.LevelError,
}

// ClassifyLine implements LineClassifier; ngrok's logfmt level is its `lvl=` field.
func (n Ngrok) ClassifyLine(line string) slog.Level {
	for _, f := range strings.Fields(line) {
		if tok, ok := strings.CutPrefix(f, "lvl="); ok {
			if lvl, ok := ngrokLevelByToken[strings.ToLower(strings.Trim(tok, `"`))]; ok {
				return lvl
			}
		}
	}
	return slog.LevelInfo
}
