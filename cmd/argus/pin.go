package main

import (
	"github.com/MunifTanjim/argus/internal/config"
	"github.com/MunifTanjim/argus/internal/trustpin"
)

// nodePinFile is the local node's genesis pin, kept beside its chain.
func nodePinFile() *trustpin.File {
	return trustpin.New(config.GetStatePath("trustlog-genesis"))
}

// clientPinFile is this device's client-role genesis pin. A machine running both
// roles pins both; they always hold the same trust root.
func clientPinFile() *trustpin.File {
	return trustpin.New(config.GetStatePath("client-trustlog-genesis"))
}

// clientHead is the client pin a client connects with: nil with E2EE off.
func clientHead(cfg *config.Config) ([]byte, error) {
	if !cfg.E2EE.Enabled {
		return nil, nil
	}
	pin, err := trustpin.Resolve(cfg.Lock.Genesis, clientPinFile())
	if err != nil {
		return nil, err
	}
	return pin.Genesis, nil
}
