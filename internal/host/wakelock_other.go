//go:build !darwin

package host

func defaultStarter() starter { return nil }
