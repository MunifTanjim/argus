package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/MunifTanjim/argus/internal/config"
)

// withConfigFile points config.ConfigDir at a temp dir holding the given config body,
// and clears $ARGUS_CONFIG so resolution uses the default-path lookup.
func withConfigFile(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	orig := config.ConfigDir
	config.ConfigDir = dir
	t.Cleanup(func() { config.ConfigDir = orig })
	t.Setenv("ARGUS_CONFIG", "")
}

func TestResolveConfigReadsFileByDefault(t *testing.T) {
	withConfigFile(t, "token: filetok\n")
	root := newRootCmd("test")
	if err := root.Flags().Parse(nil); err != nil {
		t.Fatalf("parse: %v", err)
	}
	cfg, err := resolveConfig(root)
	if err != nil {
		t.Fatalf("resolveConfig: %v", err)
	}
	if cfg.Token != "filetok" {
		t.Errorf("token = %q, want filetok from config file", cfg.Token)
	}
}

func TestResolveConfigNoConfigSkipsFile(t *testing.T) {
	withConfigFile(t, "token: filetok\n")
	root := newRootCmd("test")
	if err := root.Flags().Parse([]string{"--no-config"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	cfg, err := resolveConfig(root)
	if err != nil {
		t.Fatalf("resolveConfig: %v", err)
	}
	if cfg.Token != "" {
		t.Errorf("token = %q, want empty: --no-config must skip the file", cfg.Token)
	}
}

// cobra marks --config / --no-config mutually exclusive; the validation rides on the
// persistent flags, so it must fire on subcommands too, before RunE.
func TestNoConfigMutuallyExclusiveWithConfig(t *testing.T) {
	root := newRootCmd("test")
	root.SetOut(new(bytes.Buffer))
	root.SetErr(new(bytes.Buffer))
	root.SetArgs([]string{"config", "dir", "--no-config", "--config", "/tmp/x.yaml"})
	if err := root.Execute(); err == nil {
		t.Error("--no-config combined with --config should error")
	}
}
