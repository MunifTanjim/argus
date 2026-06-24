package main

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// TestFlagsAreMappedToConfigKeys guards the flag→config wiring footgun: a flag only
// reaches the resolved config if its name is in flagKeys (see config.go), but flags are
// defined in root.go / start.go / hooks.go. Adding a config-backed flag without a
// flagKeys entry would silently drop it. This walks every argus command and asserts each
// flag is either mapped or explicitly exempt — so a new flag forces a conscious choice.
func TestFlagsAreMappedToConfigKeys(t *testing.T) {
	// Flags that are intentionally NOT config-backed.
	exempt := map[string]bool{
		"config":   true, // selects the config file itself; resolved before viper loads
		"help":     true, // cobra builtin
		"version":  true, // cobra builtin
		"bin":      true, // `hooks install` only; not a node/client setting
		"count":    true, // `ping` only
		"interval": true, // `ping` only
		"url":      true, // `pair` only: QR base-URL override
		"timeout":  true, // `pair` only: device-connect wait
	}

	seen := map[string]bool{}
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		// Skip cobra's generated command subtrees (completion, help).
		if c.Name() == "completion" || c.Name() == "help" {
			return
		}
		c.LocalFlags().VisitAll(func(f *pflag.Flag) {
			if exempt[f.Name] || seen[f.Name] {
				return
			}
			seen[f.Name] = true
			if _, ok := flagKeys[f.Name]; !ok {
				t.Errorf("flag --%s (on %q) has no flagKeys entry: it would be silently ignored by config resolution. Add it to flagKeys, or to this test's exempt set if it is intentionally not config-backed.", f.Name, c.CommandPath())
			}
		})
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(newRootCmd("test"))
}
