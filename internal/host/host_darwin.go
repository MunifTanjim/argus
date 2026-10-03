//go:build darwin

package host

import (
	"context"
	"os/exec"
	"time"

	"golang.org/x/sys/unix"

	"github.com/MunifTanjim/argus/internal/api"
)

func Battery(ctx context.Context) (*api.HostBattery, error) {
	out, err := exec.CommandContext(ctx, "ioreg", "-rn", "AppleSmartBattery").Output()
	if err != nil {
		return nil, err
	}
	return parseIORegBattery(out), nil
}

func Uptime() (int64, error) {
	tv, err := unix.SysctlTimeval("kern.boottime")
	if err != nil {
		return 0, err
	}
	return int64(time.Since(time.Unix(tv.Unix())).Seconds()), nil
}

func OS() string {
	if v, err := unix.Sysctl("kern.osproductversion"); err == nil && v != "" {
		return withArch("macOS " + v)
	}
	return withArch("macOS")
}
