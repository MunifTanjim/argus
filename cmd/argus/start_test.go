package main

import (
	"testing"

	"github.com/MunifTanjim/argus/internal/config"
)

func cfgWith(url, token string) *config.Config {
	c := &config.Config{Token: token}
	c.Gateway.URL = url
	return c
}

func TestRoleGates(t *testing.T) {
	cases := []struct {
		name        string
		url, token  string
		wantUplink  bool
		wantGateway bool
	}{
		{"connected node", "wss://h", "tok", true, false},
		{"connected no token", "wss://h", "", true, false},
		{"gateway node", "", "tok", false, true},
		{"local node", "", "", false, false},
		{"url+token does not listen", "wss://h", "tok", true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := cfgWith(tc.url, tc.token)
			if got := uplinkMode(cfg); got != tc.wantUplink {
				t.Errorf("uplinkMode = %v, want %v", got, tc.wantUplink)
			}
			if got := gatewayMode(cfg); got != tc.wantGateway {
				t.Errorf("gatewayMode = %v, want %v", got, tc.wantGateway)
			}
		})
	}
}
