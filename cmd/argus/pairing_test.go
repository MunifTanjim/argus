package main

import "testing"

func TestPairingURI(t *testing.T) {
	cases := []struct {
		publicURL string
		token     string
		want      string
	}{
		{"https://argus.example.ts.net", "tok", "argus://pair?token=tok&url=wss%3A%2F%2Fargus.example.ts.net"},
		{"http://192.168.1.5:8443", "a/b", "argus://pair?token=a%2Fb&url=ws%3A%2F%2F192.168.1.5%3A8443"},
		// A reverse-proxy base path is preserved (app appends /client); trailing slash trimmed.
		{"wss://example.com/gateway", "tok", "argus://pair?token=tok&url=wss%3A%2F%2Fexample.com%2Fgateway"},
		{"wss://example.com/gateway/", "tok", "argus://pair?token=tok&url=wss%3A%2F%2Fexample.com%2Fgateway"},
	}
	for _, c := range cases {
		got, err := pairingURI(c.publicURL, c.token)
		if err != nil {
			t.Fatalf("pairingURI(%q): %v", c.publicURL, err)
		}
		if got != c.want {
			t.Errorf("pairingURI(%q) = %q, want %q", c.publicURL, got, c.want)
		}
	}
}

func TestPairingURIRejectsBadURL(t *testing.T) {
	cases := []struct {
		publicURL string
		desc      string
	}{
		{"://nope", "unparseable URL"},
		{"ftp://example.com", "unsupported scheme"},
		{"/relative-path", "empty host"},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			_, err := pairingURI(c.publicURL, "token")
			if err == nil {
				t.Errorf("expected error for %s", c.desc)
			}
		})
	}
}
