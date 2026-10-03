//go:build linux

package host

import (
	"context"
	"os"

	"github.com/MunifTanjim/argus/internal/api"
)

func Battery(context.Context) (*api.HostBattery, error) {
	return readSysfsBattery("/sys/class/power_supply")
}

func Uptime() (int64, error) {
	b, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0, err
	}
	return parseProcUptime(b)
}

func OS() string {
	b, _ := os.ReadFile("/etc/os-release")
	if name := osReleaseName(b); name != "" {
		return withArch(name)
	}
	return withArch("Linux")
}
