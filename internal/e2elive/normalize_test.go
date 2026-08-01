package e2elive

import (
	"strings"
	"testing"
)

func newNormCluster() *Cluster {
	return &Cluster{Root: "/var/folders/xy/abc123/T/axe999", GWAddr: "127.0.0.1:54321"}
}

func TestNormalizeSubstitutesRegisteredValues(t *testing.T) {
	c := newNormCluster()
	c.Redact("sigpub:"+strings.Repeat("a", 64), "<NODE-A-SIGPUB>")
	c.Redact(c.Root, "<ROOT>")
	c.Redact(c.GWAddr, "<GW-ADDR>")

	in := "signer: sigpub:" + strings.Repeat("a", 64) + "\nsocket: " + c.Root + "/node-a/s\ngateway: ws://" + c.GWAddr + "\n"
	got, err := c.normalize(in)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	want := "signer: <NODE-A-SIGPUB>\nsocket: <ROOT>/node-a/s\ngateway: ws://<GW-ADDR>\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestNormalizePrefersLongestMatch(t *testing.T) {
	c := newNormCluster()
	c.Redact("gen:abc", "<SHORT>")
	c.Redact("gen:abcdef", "<LONG>")

	got, err := c.normalize("x gen:abcdef y")
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if got != "x <LONG> y" {
		t.Fatalf("got %q", got)
	}
}

func TestNormalizeFallbacksTimeDurationFingerprint(t *testing.T) {
	c := newNormCluster()
	in := "at 2026-08-01T12:34:56Z took 1.25s\n  trust fingerprint: amber koala rivet dune\n"
	got, err := c.normalize(in)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	want := "at <TIME> took <DUR>\n  trust fingerprint: <FP>\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestNormalizeRejectsUnregisteredVolatileValues(t *testing.T) {
	c := newNormCluster()
	cases := map[string]string{
		"hex":    "genesis: gen:" + strings.Repeat("f", 64),
		"port":   "listening on 127.0.0.1:9999",
		"tmp":    "wrote /var/folders/zz/other/T/axe000/x",
		"base64": "blob: " + strings.Repeat("QUJD", 15) + "==",
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := c.normalize(in); err == nil {
				t.Fatalf("expected leftover-volatility failure for %q", in)
			} else if !strings.Contains(err.Error(), "unregistered volatile value") {
				t.Fatalf("wrong error: %v", err)
			}
		})
	}
}

func TestNormalizeAcceptsPlaceholdersAndPlainText(t *testing.T) {
	c := newNormCluster()
	in := "locked mode: enforcing\n  signers: 2\n  devices: 1\n  genesis: <GENESIS>\n"
	got, err := c.normalize(in)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if got != in {
		t.Fatalf("plain text changed:\n%s", got)
	}
}

func TestNormalizeRejectsSpaceSeparatedTimestamp(t *testing.T) {
	c := newNormCluster()
	in := "event occurred at 2026-08-01 12:34:56 +0000\n"
	if _, err := c.normalize(in); err == nil {
		t.Fatalf("expected leftover-volatility failure for space-separated timestamp")
	} else if !strings.Contains(err.Error(), "unregistered volatile value") {
		t.Fatalf("wrong error: %v", err)
	}
}

func TestNormalizeDurationRegexDoesNotMatchNodeLabels(t *testing.T) {
	c := newNormCluster()
	in := "node: node-3m\nstatus: online\n"
	got, err := c.normalize(in)
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if got != in {
		t.Fatalf("node label was incorrectly modified: got %q", got)
	}
}
