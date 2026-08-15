package e2elive

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// TestLockRevokeSignerCeremonyCLI drives the three-phase co-signing ceremony across
// three real processes, passing the ceremony blob between them through argv.
func TestLockRevokeSignerCeremonyCLI(t *testing.T) {
	c := New(t)
	c.StartGateway()
	a := c.AddNode("node-a")
	b := c.AddNode("node-b")
	cc := c.AddNode("node-c")
	c.WaitOnline("node-a", "node-b", "node-c")

	statusA0 := a.LockRun("status")
	statusB0 := b.LockRun("status")
	statusC0 := cc.LockRun("status")
	sigA := Scrape(t, statusA0, PatSignerKey)
	sigB := Scrape(t, statusB0, PatSignerKey)
	sigC := Scrape(t, statusC0, PatSignerKey)
	devA := Scrape(t, statusA0, PatDeviceKey)
	devB := Scrape(t, statusB0, PatDeviceKey)
	devC := Scrape(t, statusC0, PatDeviceKey)
	c.Redact(sigA, "<NODE-A-SIGPUB>")
	c.Redact(sigB, "<NODE-B-SIGPUB>")
	c.Redact(sigC, "<NODE-C-SIGPUB>")
	c.Redact(devA, "<NODE-A-DEVPUB>")
	c.Redact(devB, "<NODE-B-DEVPUB>")
	c.Redact(devC, "<NODE-C-DEVPUB>")

	initRes := a.LockRun("init", sigA, sigB, "--confirm")
	genesis := Scrape(t, initRes, PatGenesis)
	c.Redact(genesis, "<GENESIS>")
	for i, s := range ScrapeAll(t, initRes, PatDisablement) {
		c.Redact(s, "<SECRET-"+string(rune('1'+i))+">")
	}

	reTip := regexp.MustCompile(PatTip)
	reEntryHash := regexp.MustCompile(PatEntryHash)
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
		for _, m := range reEntryHash.FindAllStringSubmatch(r.Stdout+r.Stderr, -1) {
			if hash := m[1]; strings.HasPrefix(hash, "tip:") && !seenTips[hash] {
				seenTips[hash] = true
				tipN++
				c.Redact(hash, fmt.Sprintf("<TIP-%d>", tipN))
			}
		}
	}
	redactTip(initRes)

	c.Step(t, "init-two-signers", initRes)
	c.Step(t, "pin-node-b", b.LockRun("pin", genesis))
	c.WaitLockEnforcing("node-b")

	start := a.LockRun("revoke-signer", sigB, "--replacement", sigC)
	redactTip(start)
	blob1 := Scrape(t, start, PatBlob)
	c.Redact(blob1, "<BLOB-START>")
	c.Step(t, "revoke-signer-start", start)

	cosign := b.LockRun("revoke-signer", "--cosign", blob1)
	redactTip(cosign)
	blob2 := Scrape(t, cosign, PatBlob)
	c.Redact(blob2, "<BLOB-COSIGNED>")
	c.Step(t, "revoke-signer-cosign", cosign)

	finish := a.LockRun("revoke-signer", "--finish", blob2)
	redactTip(finish)
	c.Step(t, "revoke-signer-finish", finish)

	statusA := a.LockRun("status")
	redactTip(statusA)
	c.Step(t, "status-a-after-ceremony", statusA)

	c.Step(t, "pin-node-c", cc.LockRun("pin", genesis))
	c.WaitLockEnforcing("node-c")

	statusC := cc.LockRun("status")
	redactTip(statusC)
	c.Step(t, "status-c-after-ceremony", statusC)

	logRes := a.LockRun("log")
	redactTip(logRes)
	c.Step(t, "log-after-ceremony", logRes)
}
