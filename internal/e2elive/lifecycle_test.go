package e2elive

import (
	"fmt"
	"regexp"
	"testing"
)

// TestLockLifecycleCLI walks a locked network's whole life through the CLI on two
// real node processes, pinning the exit code and output of every step.
func TestLockLifecycleCLI(t *testing.T) {
	if testing.Short() {
		t.Skip("real-process e2e; skipped under -short")
	}
	c := New(t)
	c.StartGateway()
	a := c.AddNode("node-a")
	b := c.AddNode("node-b")
	c.WaitOnline("node-a", "node-b")

	statusA := a.LockRun("status")
	sigA := Scrape(t, statusA, PatSignerKey)
	devA := Scrape(t, statusA, PatDeviceKey)
	c.Redact(sigA, "<NODE-A-SIGPUB>")
	c.Redact(devA, "<NODE-A-DEVPUB>")

	statusB := b.LockRun("status")
	sigB := Scrape(t, statusB, PatSignerKey)
	devB := Scrape(t, statusB, PatDeviceKey)
	c.Redact(sigB, "<NODE-B-SIGPUB>")
	c.Redact(devB, "<NODE-B-DEVPUB>")

	c.Step(t, "status-a-unlocked", statusA)
	c.Step(t, "status-b-unlocked", statusB)

	c.Step(t, "init-dryrun", a.LockRun("init", sigA))

	initRes := a.LockRun("init", sigA, "--confirm", "--gen-disablements=2")
	genesis := Scrape(t, initRes, PatGenesis)
	secrets := ScrapeAll(t, initRes, PatDisablement)
	c.Redact(genesis, "<GENESIS>")
	for i, s := range secrets {
		c.Redact(s, "<SECRET-"+string(rune('1'+i))+">")
	}
	c.Step(t, "init-confirm", initRes)

	// Tip values appear in status output and in every write-operation confirmation.
	// Each unique tip is registered in encounter order so goldens are stable.
	reTip := regexp.MustCompile(PatTip)
	seenTips := map[string]bool{}
	tipN := 0
	redactTip := func(r Result) {
		for _, m := range reTip.FindAllStringSubmatch(r.Stdout+r.Stderr, -1) {
			if tip := m[1]; !seenTips[tip] {
				seenTips[tip] = true
				tipN++
				c.Redact(tip, fmt.Sprintf("<TIP-%d>", tipN))
			}
		}
	}

	// Node-b must be pinned before it can enforce; without this step WaitLockEnforcing
	// would never return because an unpinned node that sees the trust log quarantines
	// rather than enforcing.
	c.Step(t, "pin-node-b", b.LockRun("pin", genesis))

	lockedA := a.LockRun("status")
	redactTip(lockedA)
	c.Step(t, "status-a-locked", lockedA)

	c.WaitLockEnforcing("node-b")

	lockedB := b.LockRun("status")
	redactTip(lockedB)
	c.Step(t, "status-b-locked", lockedB)

	c.Step(t, "log-after-init", a.LockRun("log"))

	addSignerB := a.LockRun("add-signer", sigB)
	redactTip(addSignerB)
	c.Step(t, "add-signer-b", addSignerB)

	twoSigners := a.LockRun("status")
	redactTip(twoSigners)
	c.Step(t, "status-a-two-signers", twoSigners)

	// Revoke before sign: node-b's device is authorized by the genesis, so signing
	// it first would be a no-op. Revoking first makes both steps a real transition.
	revokeDevB := a.LockRun("revoke-device", "node-b")
	redactTip(revokeDevB)
	c.Step(t, "revoke-device-b", revokeDevB)

	c.WaitTip("node-b", Scrape(t, revokeDevB, PatTip))

	revokedB := b.LockRun("status")
	redactTip(revokedB)
	c.Step(t, "status-b-revoked", revokedB)

	signB := a.LockRun("sign", "node-b")
	redactTip(signB)
	c.Step(t, "sign-node-b", signB)

	c.WaitTip("node-b", Scrape(t, signB, PatTip))

	authorizedB := b.LockRun("status")
	redactTip(authorizedB)
	c.Step(t, "status-b-authorized", authorizedB)

	removeSignerB := a.LockRun("remove-signer", sigB)
	redactTip(removeSignerB)
	c.Step(t, "remove-signer-b", removeSignerB)

	oneSigner := a.LockRun("status")
	redactTip(oneSigner)
	c.Step(t, "status-a-one-signer", oneSigner)

	c.Step(t, "log-after-signer-churn", a.LockRun("log"))

	c.WaitTip("node-b", Scrape(t, removeSignerB, PatTip))

	c.Step(t, "local-disable-b", b.LockRun("local-disable"))

	localDisabledB := b.LockRun("status")
	redactTip(localDisabledB)
	c.Step(t, "status-b-local-disabled", localDisabledB)

	stillEnforcingA := a.LockRun("status")
	redactTip(stillEnforcingA)
	c.Step(t, "status-a-still-enforcing", stillEnforcingA)

	if len(secrets) == 0 {
		t.Fatalf("init produced no disablement secret")
	}

	disableNet := a.LockRun("disable", secrets[0])
	redactTip(disableNet)
	c.Step(t, "disable-network", disableNet)

	disabledA := a.LockRun("status")
	redactTip(disabledA)
	c.Step(t, "status-a-disabled", disabledA)

	c.WaitLockDisabled("node-b")

	disabledB := b.LockRun("status")
	redactTip(disabledB)
	c.Step(t, "status-b-disabled", disabledB)
}
