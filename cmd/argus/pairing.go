package main

import (
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/mdp/qrterminal/v3"
)

// pairingURI converts a gateway public URL into the mobile pairing URI
// argus://pair?url=<wss base>&token=<token>. The url keeps any reverse-proxy base path
// (trailing slash trimmed); the app appends the implicit /client route, mirroring the
// TUI's hub-url resolver (resolveGatewayURL).
func pairingURI(publicURL, token string) (string, error) {
	u, err := url.Parse(publicURL)
	if err != nil {
		return "", err
	}
	if u.Host == "" {
		return "", fmt.Errorf("pairing: public url %q has no host", publicURL)
	}
	switch u.Scheme {
	case "https", "wss":
		u.Scheme = "wss"
	case "http", "ws":
		u.Scheme = "ws"
	default:
		return "", fmt.Errorf("pairing: unsupported scheme %q", u.Scheme)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	q := url.Values{}
	q.Set("url", u.String())
	q.Set("token", token)
	return "argus://pair?" + q.Encode(), nil
}

// printPairingQR renders the pairing URI as a terminal QR plus the raw line (scan or
// copy). Callers print their own surrounding context.
func printPairingQR(w io.Writer, publicURL, token string) error {
	uri, err := pairingURI(publicURL, token)
	if err != nil {
		return err
	}
	qrterminal.GenerateHalfBlock(uri, qrterminal.L, w)
	fmt.Fprintf(w, "%s\n", uri)
	return nil
}
