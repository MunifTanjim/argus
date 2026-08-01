package e2elive

import (
	"strings"
	"testing"
)

const sampleStatus = `locked mode: not enabled
  this node signer: sigpub:1111111111111111111111111111111111111111111111111111111111111111
  this node identity: devpub:2222222222222222222222222222222222222222222222222222222222222222
`

const sampleInit = `locked mode enabled
  genesis: gen:3333333333333333333333333333333333333333333333333333333333333333
  signers: 1
  disablement secret: dis:4444444444444444444444444444444444444444444444444444444444444444
  disablement secret: dis:5555555555555555555555555555555555555555555555555555555555555555
`

func TestScrapePullsKeysFromStatus(t *testing.T) {
	r := Result{Stdout: sampleStatus}
	if got := Scrape(t, r, PatSignerKey); !strings.HasPrefix(got, "sigpub:1111") {
		t.Fatalf("signer key = %q", got)
	}
	if got := Scrape(t, r, PatDeviceKey); !strings.HasPrefix(got, "devpub:2222") {
		t.Fatalf("device key = %q", got)
	}
}

func TestScrapeAllPullsEveryDisablementSecret(t *testing.T) {
	got := ScrapeAll(t, Result{Stdout: sampleInit}, PatDisablement)
	if len(got) != 2 {
		t.Fatalf("got %d secrets: %v", len(got), got)
	}
	if !strings.HasPrefix(got[1], "dis:5555") {
		t.Fatalf("second secret = %q", got[1])
	}
}

func TestScrapeSearchesStderrToo(t *testing.T) {
	r := Result{Stderr: "Next: run on another signer node:\n  argus lock revoke-signer --cosign QUJDREVG\n"}
	if got := Scrape(t, r, PatBlob); got != "QUJDREVG" {
		t.Fatalf("blob = %q", got)
	}
}

func TestScrapeFailsWhenValueAbsent(t *testing.T) {
	fake := &testing.T{}
	if got := Scrape(fake, Result{Stdout: "nothing here\n"}, PatGenesis); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
	if !fake.Failed() {
		t.Fatalf("absent value did not fail the test")
	}
}
