package e2elive

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

// lockedPair brings up a gateway and two nodes, locks the network with node-a as
// the sole signer, pins node-b to it, and returns the two nodes plus a redactor
// that registers every tip it is shown. It exists so the restart tests start from
// the state a reboot is interesting in — a converged, enforcing peer — without
// each of them re-deriving it.
func lockedPair(t *testing.T, c *Cluster) (a, b *Node, redactTip func(Result)) {
	t.Helper()
	c.StartGateway()
	a = c.AddNode("node-a")
	b = c.AddNode("node-b")
	c.WaitOnline("node-a", "node-b")

	statusA := a.LockRun("status")
	sigA := Scrape(t, statusA, PatSignerKey)
	c.Redact(sigA, "<NODE-A-SIGPUB>")
	c.Redact(Scrape(t, statusA, PatDeviceKey), "<NODE-A-DEVPUB>")
	statusB := b.LockRun("status")
	c.Redact(Scrape(t, statusB, PatSignerKey), "<NODE-B-SIGPUB>")
	c.Redact(Scrape(t, statusB, PatDeviceKey), "<NODE-B-DEVPUB>")

	reTip := regexp.MustCompile(PatTip)
	seen := map[string]bool{}
	n := 0
	redactTip = func(r Result) {
		for _, m := range reTip.FindAllStringSubmatch(r.Stdout+r.Stderr, -1) {
			if tip := m[1]; !seen[tip] {
				seen[tip] = true
				n++
				c.Redact(tip, fmt.Sprintf("<TIP-%d>", n))
			}
		}
	}

	initRes := a.LockRun("init", sigA, "--confirm")
	genesis := Scrape(t, initRes, PatGenesis)
	c.Redact(genesis, "<GENESIS>")
	for i, s := range ScrapeAll(t, initRes, PatDisablement) {
		c.Redact(s, "<SECRET-"+string(rune('1'+i))+">")
	}
	redactTip(initRes)

	c.Step(t, "pin-node-b", b.LockRun("pin", genesis))
	c.WaitLockEnforcing("node-b")
	return a, b, redactTip
}

// TestNodeRestartKeepsLockCLI answers the question an operator has after rebooting
// a machine: is the node still locked, and do I have to pin it again? It must come
// back enforcing at the tip it went down on, from its own disk, with no `lock pin`
// run in between — and it must still be a live participant afterwards, not just a
// process holding stale state.
func TestNodeRestartKeepsLockCLI(t *testing.T) {
	c := New(t)
	a, b, redactTip := lockedPair(t, c)

	before := b.LockRun("status")
	redactTip(before)
	c.Step(t, "status-b-before-restart", before)
	tip := Scrape(t, before, PatTip)

	c.RestartNode("node-b")

	c.WaitTip("node-b", tip)
	after := b.LockRun("status")
	redactTip(after)
	if !strings.Contains(after.Stdout, "locked mode: enforcing") {
		t.Fatalf("node-b came back open after a restart:\n%s", after.Stdout)
	}
	if strings.Contains(after.Stdout, "pin: none") {
		t.Fatalf("node-b lost its pin across a restart:\n%s", after.Stdout)
	}
	c.Step(t, "status-b-after-restart", after)

	// Reloading the pin is not enough on its own: the restarted node has to be
	// syncing again too, so a write on node-a must still reach it.
	revoke := a.LockRun("revoke-device", "node-b")
	redactTip(revoke)
	c.Step(t, "revoke-device-b-after-restart", revoke)

	c.WaitTip("node-b", Scrape(t, revoke, PatTip))
	converged := b.LockRun("status")
	redactTip(converged)
	c.Step(t, "status-b-converged-after-restart", converged)
}

// TestGatewayRestartKeepsFleetLockedCLI reboots the gateway host. Two things must
// hold: losing the relay does not unlock anything (a blind gateway is not what
// enforces), and once it is back the fleet refills its empty entry store by itself,
// so a write made after the restart still reaches a peer.
func TestGatewayRestartKeepsFleetLockedCLI(t *testing.T) {
	c := New(t)
	a, b, redactTip := lockedPair(t, c)

	c.StopGateway()

	down := b.LockRun("status")
	redactTip(down)
	if !strings.Contains(down.Stdout, "locked mode: enforcing") {
		t.Fatalf("node-b stopped enforcing when the gateway went away:\n%s", down.Stdout)
	}
	c.Step(t, "status-b-gateway-down", down)

	c.StartGateway()
	c.WaitOnline("node-a", "node-b")

	// The replacement gateway holds no entries. Node-a re-offers its chain on
	// reconnect, which is the only reason this write can reach node-b at all.
	revoke := a.LockRun("revoke-device", "node-b")
	redactTip(revoke)
	c.Step(t, "revoke-device-b-after-gw-restart", revoke)

	c.WaitTip("node-b", Scrape(t, revoke, PatTip))
	converged := b.LockRun("status")
	redactTip(converged)
	c.Step(t, "status-b-converged-after-gw-restart", converged)
}

// TestNodeRestartWithCorruptChainCLI corrupts the persisted chain while the node is
// down. The pin is what makes the network locked for this device, and it lives in a
// separate file — so an unreadable chain must not come back as an open node. The
// node is expected to start pinned with nothing loaded and refill from the gateway.
func TestNodeRestartWithCorruptChainCLI(t *testing.T) {
	c := New(t)
	_, b, redactTip := lockedPair(t, c)

	before := b.LockRun("status")
	redactTip(before)
	tip := Scrape(t, before, PatTip)

	c.StopNode("node-b")
	chain := b.StatePath("trustlog-chain")
	if _, err := os.Stat(chain); err != nil {
		t.Fatalf("expected a persisted chain at %s: %v", chain, err)
	}
	if err := os.WriteFile(chain, []byte("not a trust-log chain"), 0o600); err != nil {
		t.Fatalf("corrupt chain: %v", err)
	}
	c.StartNode("node-b")

	// Without this the test would still pass if the corruption never reached the
	// boot path, and would quietly stop covering anything.
	waitFor(t, "node-b reports the unusable chain", func() bool {
		return strings.Contains(b.Log(), "ignoring unusable persisted trust-log chain")
	})

	c.WaitTip("node-b", tip)
	recovered := b.LockRun("status")
	redactTip(recovered)
	if !strings.Contains(recovered.Stdout, "locked mode: enforcing") {
		t.Fatalf("a corrupt chain must not leave node-b open:\n%s", recovered.Stdout)
	}
	c.Step(t, "status-b-after-corrupt-chain", recovered)
}
