package tunnel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
)

// Zrok runs a zrok v2 (`zrok2`) public share exposing the gateway at a stable URL, backed
// by a reserved name in a namespace: `zrok2 share public <origin> -n <ns>:<name>` yields
// https://<name>.<ns-domain> (e.g. https://<name>.shares.zrok.io).
//
// The environment must be enabled by the caller (ensureZrokEnabled). A pointer receiver
// lets ExtractURL match the host Prepare resolves.
type Zrok struct {
	Bin       string // path to the zrok2 binary
	Selection string // reserved name selection: "namespace:name" or "name" (ns defaults to "public")

	runner cmdRunner // nil => defaultRunner (real exec); overridable in tests
	host   string    // public host "<name>.<domain>", resolved in Prepare
}

func (z *Zrok) Name() string { return "zrok" }

// namespaceAndName splits Selection on the first ":" into (namespace, name); a bare name
// defaults to the "public" namespace (shares.zrok.io).
func (z *Zrok) namespaceAndName() (namespace, name string) {
	if ns, n, ok := strings.Cut(z.Selection, ":"); ok {
		return ns, n
	}
	return "public", z.Selection
}

func (z *Zrok) Command(origin string) (CommandSpec, error) {
	ns, name := z.namespaceAndName()
	// Default backend mode (proxy) is fine; --headless sends output to stdout/stderr.
	args := []string{"share", "public", origin, "--headless", "-n", ns + ":" + name}
	return CommandSpec{Path: z.Bin, Args: args}, nil
}

// ExtractURL returns the share's public URL once zrok announces its endpoint — which it
// does only when the share is live, so this doubles as the readiness signal. The endpoint
// is printed as the bare host resolved in Prepare (no scheme); return it as https.
func (z *Zrok) ExtractURL(line string) (string, bool) {
	if z.host == "" || !strings.Contains(line, z.host) {
		return "", false
	}
	return "https://" + z.host, true
}

// zrokLevelByToken maps zrok's level words to slog levels. INFO is demoted to Debug:
// zrok's steady-state info is chatty noise, and argus reports the public URL itself
// (via ExtractURL) regardless of this level.
var zrokLevelByToken = map[string]slog.Level{
	"TRACE":   slog.LevelDebug,
	"DEBUG":   slog.LevelDebug,
	"INFO":    slog.LevelDebug,
	"WARN":    slog.LevelWarn,
	"WARNING": slog.LevelWarn,
	"ERROR":   slog.LevelError,
	"FATAL":   slog.LevelError,
	"PANIC":   slog.LevelError,
}

// ClassifyLine implements LineClassifier, covering both zrok's pfxlog text format
// ("[ 0.123] INFO …") and a JSON line ({"level":"info",…}). Unrecognized lines default
// to Info.
func (z *Zrok) ClassifyLine(line string) slog.Level {
	if lvl, ok := zrokJSONLevel(line); ok {
		return lvl
	}
	for i, f := range strings.Fields(line) {
		if i >= 4 {
			break
		}
		if lvl, ok := zrokLevelByToken[strings.ToUpper(f)]; ok {
			return lvl
		}
	}
	return slog.LevelInfo
}

func zrokJSONLevel(line string) (slog.Level, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "{") {
		return 0, false
	}
	var rec struct {
		Level string `json:"level"`
	}
	if err := json.Unmarshal([]byte(line), &rec); err != nil || rec.Level == "" {
		return 0, false
	}
	lvl, ok := zrokLevelByToken[strings.ToUpper(rec.Level)]
	return lvl, ok
}

// Prepare implements LifecycleProvider: ready the reserved name for a fresh share and
// resolve the public host (for ExtractURL). It returns "" — the URL is reported only once
// zrok prints its endpoint (the share is live).
//
// A reserved name holds one share at a time; a run killed before releasing leaves a stale
// share bound to it, so the next `share public` 409s. So: create the name if absent, else
// delete any share still bound to it.
func (z *Zrok) Prepare(ctx context.Context) (string, error) {
	ns, name := z.namespaceAndName()
	found, existingShareToken, err := z.findName(ctx, ns, name)
	if err != nil {
		return "", err
	}
	if !found {
		if _, stderr, err := z.exec(ctx, "create", "name", "-n", ns, name); err != nil && !isZrokNameConflict(stderr) {
			return "", fmt.Errorf("zrok2 create name %s: %w: %s", name, err, bytes.TrimSpace(stderr))
		}
	} else if existingShareToken != "" {
		// Free the name from a stale share left by a prior run. Tolerate a 404 (the
		// share was already released between findName and here — the name is free).
		if _, stderr, err := z.exec(ctx, "delete", "share", existingShareToken); err != nil && !isZrokShareNotFound(stderr) {
			return "", fmt.Errorf("zrok2 delete stale share %s: %w: %s", existingShareToken, err, bytes.TrimSpace(stderr))
		}
	}

	domain, err := z.namespaceDomain(ctx, ns)
	if err != nil {
		return "", err
	}
	z.host = name + "." + domain
	return "", nil
}

// findName looks up name in namespace ns via `zrok2 list names -n <ns> --json`, returning
// whether it exists and the share token currently bound to it ("" if none).
func (z *Zrok) findName(ctx context.Context, ns, name string) (found bool, shareToken string, err error) {
	stdout, stderr, err := z.exec(ctx, "list", "names", "-n", ns, "--json")
	if err != nil {
		return false, "", fmt.Errorf("zrok2 list names: %w: %s", err, bytes.TrimSpace(stderr))
	}
	var names []struct {
		Name       string `json:"name"`
		ShareToken string `json:"shareToken"`
	}
	if err := json.Unmarshal(stdout, &names); err != nil {
		return false, "", fmt.Errorf("parse zrok name list: %w", err)
	}
	for _, n := range names {
		if n.Name == name {
			return true, n.ShareToken, nil
		}
	}
	return false, "", nil
}

// namespaceDomain returns the public domain for namespace token ns (e.g. "shares.zrok.io"
// for "public"), via `zrok2 list namespaces --json`. The share host is <name>.<domain>.
func (z *Zrok) namespaceDomain(ctx context.Context, ns string) (string, error) {
	stdout, stderr, err := z.exec(ctx, "list", "namespaces", "--json")
	if err != nil {
		return "", fmt.Errorf("zrok2 list namespaces: %w: %s", err, bytes.TrimSpace(stderr))
	}
	var namespaces []struct {
		Name           string `json:"name"`
		NamespaceToken string `json:"namespaceToken"`
	}
	if err := json.Unmarshal(stdout, &namespaces); err != nil {
		return "", fmt.Errorf("parse zrok namespaces: %w", err)
	}
	for _, n := range namespaces {
		if n.NamespaceToken == ns {
			return n.Name, nil
		}
	}
	return "", fmt.Errorf("zrok namespace %q not found", ns)
}

// isZrokNameConflict reports whether stderr is a "name already taken" 409, e.g.
// "unable to create name (...[409] createShareNameConflict ...)" — tolerated as a race
// when a check-then-create loses to a concurrent creator.
func isZrokNameConflict(stderr []byte) bool {
	return strings.Contains(strings.ToLower(string(stderr)), "conflict")
}

// isZrokShareNotFound reports whether stderr is a "share doesn't exist" 404, e.g.
// "unable to delete share (...[404] unshareNotFound ...)" — tolerated when a share
// listed by findName is gone by the time we delete it (a prior run's cleanup, or a race).
func isZrokShareNotFound(stderr []byte) bool {
	return strings.Contains(strings.ToLower(string(stderr)), "unsharenotfound")
}

func (z *Zrok) exec(ctx context.Context, args ...string) (stdout, stderr []byte, err error) {
	run := z.runner
	if run == nil {
		run = defaultRunner
	}
	return run(ctx, z.Bin, args...)
}
