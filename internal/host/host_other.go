//go:build !darwin && !linux

package host

import (
	"context"
	"runtime"

	"github.com/MunifTanjim/argus/internal/api"
)

func Battery(context.Context) (*api.HostBattery, error) { return nil, nil }

func Uptime() (int64, error) { return 0, nil }

func OS() string { return withArch(runtime.GOOS) }
