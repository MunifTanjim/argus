package main

import (
	"fmt"
	"log/slog"

	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/e2e"
	"github.com/MunifTanjim/argus/internal/node"
	"github.com/MunifTanjim/argus/internal/trustpin"
)

// configureNodeLock loads the node's e2ee identity, then activates locked mode
// from the resolved genesis pin. Call it only when cfg.E2EE.Enabled is true.
//
// It fails CLOSED on the identity: when e2ee is enabled the identity must load,
// because a node left with the trust log on but encryption off serves
// unauthenticated plaintext channels — a locked-mode bypass.
func configureNodeLock(d *node.Node, cfg *config.Config, log *slog.Logger) error {
	kp, err := e2e.LoadOrCreateIdentity(config.GetStatePath("node-identity.json"))
	if err != nil {
		return fmt.Errorf("e2ee enabled but identity unavailable (refusing to start open): %w", err)
	}
	d.SetIdentityKey(kp)
	d.SetE2EE(true)

	pin, perr := trustpin.Resolve(cfg.Lock.Genesis, nodePinFile())
	if perr != nil {
		return fmt.Errorf("refusing to start open: %w", perr)
	}
	d.SetPinSource(pin.Source.String())
	chainPath := config.GetStatePath("trustlog-chain")
	if head := pin.Genesis; len(head) > 0 {
		if err := d.EnableTrustLog(head, chainPath); err != nil {
			return fmt.Errorf("locked mode configured but enabling trust log failed (refusing to start open): %w", err)
		}
	} else {
		d.SetTrustChainPath(chainPath)
	}
	return nil
}
