package pi

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/MunifTanjim/argus/internal/config"
)

func TestMain(m *testing.M) {
	// Tests that start real relays run this binary as the relay and as pi.
	if os.Getenv("ARGUS_TEST_PI_HOST") == "1" && len(os.Args) > 1 && os.Args[1] == "pi-host" {
		os.Exit(testHostMain(os.Args[2:]))
	}
	if os.Getenv("ARGUS_TEST_FAKE_PI") == "1" && slices.Contains(os.Args, "--mode") {
		os.Exit(fakePiMain(os.Args[1:]))
	}
	dir, err := os.MkdirTemp("", "argus-pi-cache-")
	if err != nil {
		panic(err)
	}
	config.CacheDir = dir
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// testHostMain parses `--socket <path> [--cwd <dir>] -- <argv...>` as
// `argus pi-host` does.
func testHostMain(args []string) int {
	sep := slices.Index(args, "--")
	if sep < 0 {
		return 2
	}
	flags := map[string]string{}
	for i := 0; i+1 < sep; i += 2 {
		flags[args[i]] = args[i+1]
	}
	if flags["--socket"] == "" {
		return 2
	}
	return RunHost(flags["--socket"], flags["--cwd"], args[sep+1:])
}

// fakePiMain answers get_state with the --session-id and model m1, and echoes
// a prompt back as the session name.
func fakePiMain(args []string) int {
	var id string
	if i := slices.Index(args, "--session-id"); i >= 0 && i+1 < len(args) {
		id = args[i+1]
	}
	dec := json.NewDecoder(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for {
		var rec map[string]any
		if dec.Decode(&rec) != nil {
			return 0
		}
		resp := map[string]any{"id": rec["id"], "type": "response", "command": rec["type"], "success": true}
		switch rec["type"] {
		case "get_state":
			resp["data"] = map[string]any{
				"sessionId": id, "sessionFile": "/tmp/" + id + ".jsonl",
				"model": map[string]any{"id": "m1"}, "isStreaming": false,
			}
		case "prompt":
			enc.Encode(map[string]any{"type": "session_info_changed", "name": rec["message"]})
			resp["data"] = map[string]any{"disposition": "started"}
		}
		enc.Encode(resp)
	}
}
