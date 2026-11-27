// Package tailnet identifies tailnet peers through the local tailscaled.
package tailnet

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"tailscale.com/client/local"
	"tailscale.com/client/tailscale/apitype"
	"tailscale.com/ipn/ipnstate"
)

// Daemon is the subset of the tailscaled local API used here.
type Daemon interface {
	WhoIs(ctx context.Context, remoteAddr string) (*apitype.WhoIsResponse, error)
	Status(ctx context.Context) (*ipnstate.Status, error)
}

// Local returns a Daemon backed by the system tailscaled.
func Local() Daemon { return &local.Client{} }

const (
	peerRetryWindow   = 500 * time.Millisecond
	peerRetryInterval = 50 * time.Millisecond
)

// Peer reports whether remoteAddr ("ip:port") belongs to a tailnet peer. In
// userspace-networking mode tailscaled registers a forwarded connection just
// after it connects, so not-found is retried briefly. Other errors fail fast.
func Peer(ctx context.Context, d Daemon, remoteAddr string) (bool, error) {
	deadline := time.Now().Add(peerRetryWindow)
	for {
		_, err := d.WhoIs(ctx, remoteAddr)
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, local.ErrPeerNotFound) {
			return false, err
		}
		if !time.Now().Add(peerRetryInterval).Before(deadline) {
			return false, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(peerRetryInterval):
		}
	}
}

// Dialable reports whether tailscaled is running and host is a tailnet peer.
// host is an IP or a MagicDNS name; names are resolved against the tailnet's
// peer list, not system DNS. It is a single-shot dial-time gate: no retry, and
// a daemon error reads as not dialable.
func Dialable(ctx context.Context, d Daemon, host string) bool {
	st, err := d.Status(ctx)
	if err != nil || st == nil || st.BackendState != "Running" {
		return false
	}
	addr := host
	if _, err := netip.ParseAddr(host); err != nil {
		ip, ok := peerIP(st, host)
		if !ok {
			return false
		}
		addr = ip.String()
	}
	_, err = d.WhoIs(ctx, addr)
	return err == nil
}

func peerIP(st *ipnstate.Status, name string) (netip.Addr, bool) {
	name = strings.TrimSuffix(name, ".")
	for _, p := range st.Peer {
		if p != nil && len(p.TailscaleIPs) > 0 && strings.EqualFold(strings.TrimSuffix(p.DNSName, "."), name) {
			return p.TailscaleIPs[0], true
		}
	}
	return netip.Addr{}, false
}

// Advertised returns the direct endpoint URLs this node announces: advertise
// if set, else ws://<addr>/p2p for each listen address. Only hosts that are
// this node's tailnet IPs or DNS name are kept.
func Advertised(ctx context.Context, d Daemon, listen, advertise []string, log *slog.Logger) []string {
	cands := advertise
	if len(cands) == 0 {
		for _, l := range listen {
			cands = append(cands, "ws://"+l+"/p2p")
		}
	}
	if len(cands) == 0 {
		return nil
	}
	st, err := d.Status(ctx)
	if err != nil || st == nil || st.Self == nil {
		log.Warn("tailscaled unreachable; advertising no direct endpoints", "err", err)
		return nil
	}
	var out []string
	for _, c := range cands {
		u, err := url.Parse(c)
		if err != nil || u.Hostname() == "" {
			log.Warn("dropping invalid direct endpoint", "endpoint", c)
			continue
		}
		if !ownHost(st.Self, u.Hostname()) {
			log.Warn("dropping direct endpoint: host is not this node's tailnet address", "endpoint", c)
			continue
		}
		out = append(out, c)
	}
	return out
}

func ownHost(self *ipnstate.PeerStatus, host string) bool {
	if ip, err := netip.ParseAddr(host); err == nil {
		for _, a := range self.TailscaleIPs {
			if a == ip {
				return true
			}
		}
		return false
	}
	dns := strings.TrimSuffix(self.DNSName, ".")
	return dns != "" && strings.EqualFold(host, dns)
}
