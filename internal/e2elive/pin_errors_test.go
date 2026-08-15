package e2elive

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// TestLockPinAndErrorsCLI pins the failure surface of `argus lock` — exit codes and
// error text — plus the client pin lifecycle, on one cluster in state order.
func TestLockPinAndErrorsCLI(t *testing.T) {
	c := New(t)
	c.StartGateway()
	a := c.AddNode("node-a")
	b := c.AddNode("node-b")
	c.WaitOnline("node-a", "node-b")

	statusA := a.LockRun("status")
	sigA := Scrape(t, statusA, PatSignerKey)
	c.Redact(sigA, "<NODE-A-SIGPUB>")
	c.Redact(Scrape(t, statusA, PatDeviceKey), "<NODE-A-DEVPUB>")
	statusB := b.LockRun("status")
	sigB := Scrape(t, statusB, PatSignerKey)
	c.Redact(sigB, "<NODE-B-SIGPUB>")
	c.Redact(Scrape(t, statusB, PatDeviceKey), "<NODE-B-DEVPUB>")

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

	c.Redact("127.0.0.1:1", "<UNREACHABLE-GW-ADDR>")
	c.Redact(strings.Repeat("0", 64), "<NULL-KEY>")
	c.Redact(strings.Repeat("9", 64), "<FAKE-SECRET>")

	// Phase 1 — unlocked network and connectivity failures.
	c.Step(t, "p1-status-unlocked", statusA)
	c.Step(t, "p1-log-unlocked", a.LockRun("log"))
	c.Step(t, "p1-sign-while-unlocked", a.LockRun("sign", "node-b"))
	c.Step(t, "p1-disable-while-unlocked", a.LockRun("disable", "somesecret"))
	c.Step(t, "p1-bad-token", a.LockRun("status", "--token=wrong-token"))
	c.Step(t, "p1-unreachable-gateway", a.LockRun("status", "--gateway=ws://127.0.0.1:1"))

	// When the socket is unreachable the CLI falls back to client mode via the
	// gateway, which generates an ephemeral per-run device key that must be
	// redacted before the golden comparison.
	unreachableSocket := a.LockRun("status", "--socket="+containerHome+"/nope.sock")
	for _, m := range regexp.MustCompile(PatClientDeviceKey).FindAllStringSubmatch(unreachableSocket.Stdout+unreachableSocket.Stderr, -1) {
		c.Redact(m[1], "<CLI-CLIENT-DEVPUB>")
	}
	c.Step(t, "p1-unreachable-socket", unreachableSocket)

	// Phase 2 — argument validation; no state change, so order is free.
	c.Step(t, "p2-init-no-args", a.LockRun("init"))
	c.Step(t, "p2-init-malformed-key", a.LockRun("init", "sigpub:zzzz"))
	c.Step(t, "p2-add-signer-malformed", a.LockRun("add-signer", "sigpub:nothex"))
	c.Step(t, "p2-sign-unknown-device", a.LockRun("sign", "nosuchdevice"))
	c.Step(t, "p2-revoke-unknown-device", a.LockRun("revoke-device", "devpub:"+strings.Repeat("0", 64)))
	c.Step(t, "p2-pin-garbage", a.LockRun("pin", "gen:garbage"))
	c.Step(t, "p2-unpin-not-pinned", a.LockRun("unpin"))
	c.Step(t, "p2-cosign-garbage", a.LockRun("revoke-signer", "--cosign", "!!!notbase64!!!"))

	// Phase 3 — pin lifecycle: pin, then re-init to supersede the pinned genesis.
	first := a.LockRun("init", sigA, "--confirm")
	redactTip(first)
	gen1 := Scrape(t, first, PatGenesis)
	secrets1 := ScrapeAll(t, first, PatDisablement)
	c.Redact(gen1, "<GENESIS-1>")
	for i, s := range secrets1 {
		c.Redact(s, "<SECRET-1-"+string(rune('1'+i))+">")
	}
	c.Step(t, "p3-init-first", first)

	pinRes := a.LockRun("pin", gen1)
	redactTip(pinRes)
	c.Step(t, "p3-pin", pinRes)
	c.WaitLockEnforcing("node-a")

	// Node-b also pins gen1 so it will show the superseded-pin quarantine state
	// after gen2 is created. Node-a auto-repins during re-init, so it cannot be
	// used for this check.
	pinBRes := b.LockRun("pin", gen1)
	redactTip(pinBRes)
	c.Step(t, "p3-pin-b", pinBRes)

	statusPinned := a.LockRun("status")
	redactTip(statusPinned)
	c.Step(t, "p3-status-pinned", statusPinned)

	if len(secrets1) == 0 {
		t.Fatalf("first init produced no disablement secret")
	}
	disableFirst := a.LockRun("disable", secrets1[0])
	redactTip(disableFirst)
	c.Step(t, "p3-disable-first", disableFirst)

	c.WaitLockDisabled("node-a")
	c.WaitLockDisabled("node-b")

	second := a.LockRun("init", sigA, "--confirm")
	redactTip(second)
	gen2 := Scrape(t, second, PatGenesis)
	for i, s := range ScrapeAll(t, second, PatDisablement) {
		c.Redact(s, "<SECRET-2-"+string(rune('1'+i))+">")
	}
	c.Redact(gen2, "<GENESIS-2>")
	c.Step(t, "p3-reinit-new-genesis", second)

	// Node-b is still pinned to gen1, which is now superseded by gen2. Its status
	// must show the quarantine/supersession headline, and the pin it advises must
	// name the live root — not the dead one it is still following.
	c.WaitLockQuarantined("node-b")

	statusSuperseded := b.LockRun("status")
	redactTip(statusSuperseded)
	c.Step(t, "p3-status-superseded-pin", statusSuperseded)

	unpinRes := b.LockRun("unpin")
	redactTip(unpinRes)
	c.Step(t, "p3-unpin", unpinRes)

	statusUnpinned := b.LockRun("status")
	redactTip(statusUnpinned)
	c.Step(t, "p3-status-unpinned", statusUnpinned)

	// Phase 4 — precondition failures against the now-locked network.
	//
	// p4-init-already-locked: the CLI should reject this with a non-zero exit.
	// If it succeeds, that is a real bug; the volatile values are still redacted
	// so the golden records the exact (buggy) output rather than aborting.
	p4Init := a.LockRun("init", sigA, "--confirm")
	redactTip(p4Init)
	reGen := regexp.MustCompile(PatGenesis)
	reDis := regexp.MustCompile(PatDisablement)
	if m := reGen.FindStringSubmatch(p4Init.Stdout + p4Init.Stderr); m != nil {
		c.Redact(m[1], "<GENESIS-UNEXPECTED>")
		for i, ms := range reDis.FindAllStringSubmatch(p4Init.Stdout+p4Init.Stderr, -1) {
			c.Redact(ms[1], fmt.Sprintf("<SECRET-UNEXPECTED-%d>", i+1))
		}
	}
	c.Step(t, "p4-init-already-locked", p4Init)
	c.Step(t, "p4-disable-wrong-secret", a.LockRun("disable", "dis:"+strings.Repeat("9", 64)))

	start := a.LockRun("revoke-signer", sigA, "--replacement", sigB)
	redactTip(start)
	blob := Scrape(t, start, PatBlob)
	c.Redact(blob, "<BLOB-START>")
	c.Step(t, "p4-revoke-signer-start", start)
	c.Step(t, "p4-finish-insufficient-cosigns", a.LockRun("revoke-signer", "--finish", blob))
}
