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
