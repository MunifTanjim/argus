package pi

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/MunifTanjim/argus/internal/adapter"
)

func TestAdapterShape(t *testing.T) {
	a := New()
	if a.Agent() != "pi" {
		t.Fatalf("Agent() = %q", a.Agent())
	}
	if !a.IsHeadless() {
		t.Fatal("pi adapter must be headless")
	}
	if name, _ := a.SpawnCommand(adapter.SpawnOptions{Prompt: "x"}); name != "pi" {
		t.Fatalf("SpawnCommand binary = %q; want pi (PATH probe)", name)
	}
	if name, args, ok := a.ResumeCommand("s1"); !ok || name != "pi" || len(args) != 2 || args[0] != "--session" || args[1] != "s1" {
		t.Fatalf("ResumeCommand = %q %v %v", name, args, ok)
	}
}

func TestProjectGroupDir(t *testing.T) {
	if got := projectGroupDir("/work/proj"); got != "--work-proj--" {
		t.Fatalf("group = %q", got)
	}
	if got := projectGroupDir("/mnt/ephemeral/tmp/x"); got != "--mnt-ephemeral-tmp-x--" {
		t.Fatalf("group = %q", got)
	}
}

func TestPiDirsFollowPiEnv(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, tc := range []struct{ agentDir, sessionDir, wantHome, wantSessions string }{
		{"", "", filepath.Join(home, ".pi", "agent"), filepath.Join(home, ".pi", "agent", "sessions")},
		{"/opt/pi", "", "/opt/pi", "/opt/pi/sessions"},
		{"~/pi", "~/s", filepath.Join(home, "pi"), filepath.Join(home, "s")},
		{"~", "", home, filepath.Join(home, "sessions")},
	} {
		t.Setenv("PI_CODING_AGENT_DIR", tc.agentDir)
		t.Setenv("PI_CODING_AGENT_SESSION_DIR", tc.sessionDir)
		if got, err := piHome(); err != nil || got != tc.wantHome {
			t.Errorf("PI_CODING_AGENT_DIR=%q: piHome = %q, %v; want %q", tc.agentDir, got, err, tc.wantHome)
		}
		if got, err := sessionsRoot(); err != nil || got != tc.wantSessions {
			t.Errorf("PI_CODING_AGENT_SESSION_DIR=%q: sessionsRoot = %q, %v; want %q", tc.sessionDir, got, err, tc.wantSessions)
		}
	}
}

func TestSessionsRootReadsSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	agent := filepath.Join(home, "agent")
	t.Setenv("PI_CODING_AGENT_DIR", agent)
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "")
	if err := os.MkdirAll(agent, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agent, "settings.json"), []byte("\xef\xbb\xbf"+`{"sessionDir":"~/s"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := sessionsRoot(); err != nil || got != filepath.Join(home, "s") {
		t.Fatalf("sessionsRoot = %q, %v", got, err)
	}
	t.Setenv("PI_CODING_AGENT_SESSION_DIR", "/env/s")
	if got, err := sessionsRoot(); err != nil || got != "/env/s" {
		t.Fatalf("env must win over settings: sessionsRoot = %q, %v", got, err)
	}
}
