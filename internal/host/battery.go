// Package host reads data about the machine a node runs on and holds its
// wakelock.
package host

import (
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/MunifTanjim/argus/internal/api"
)

// Only top-level properties sit alone on a line; nested dicts are printed
// inline, so their keys never match.
var ioregProp = regexp.MustCompile(`^\s*"([^"]+)" = (.*)$`)

func parseIORegBattery(out []byte) *api.HostBattery {
	props := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		m := ioregProp.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if _, seen := props[m[1]]; !seen {
			props[m[1]] = strings.TrimSpace(m[2])
		}
	}
	cur, err := strconv.Atoi(props["CurrentCapacity"])
	if err != nil {
		return nil
	}
	full, err := strconv.Atoi(props["MaxCapacity"])
	if err != nil || full <= 0 {
		return nil
	}
	state := "discharging"
	switch {
	case props["FullyCharged"] == "Yes":
		state = "full"
	case props["IsCharging"] == "Yes":
		state = "charging"
	case props["ExternalConnected"] == "Yes":
		state = "not_charging"
	}
	return &api.HostBattery{Percent: clampPercent(int(math.Round(float64(cur) * 100 / float64(full)))), State: state}
}

func clampPercent(p int) int { return min(max(p, 0), 100) }

// readSysfsBattery ignores batteries with scope=Device (mice, keyboards).
func readSysfsBattery(root string) (*api.HostBattery, error) {
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		dir := filepath.Join(root, e.Name())
		if sysfsAttr(dir, "type") != "Battery" || sysfsAttr(dir, "scope") == "Device" {
			continue
		}
		pct, err := strconv.Atoi(sysfsAttr(dir, "capacity"))
		if err != nil {
			continue
		}
		return &api.HostBattery{Percent: clampPercent(pct), State: sysfsState(sysfsAttr(dir, "status"))}, nil
	}
	return nil, nil
}

func sysfsAttr(dir, name string) string {
	b, _ := os.ReadFile(filepath.Join(dir, name))
	return strings.TrimSpace(string(b))
}

func sysfsState(status string) string {
	switch status {
	case "Charging":
		return "charging"
	case "Discharging":
		return "discharging"
	case "Full":
		return "full"
	case "Not charging":
		return "not_charging"
	}
	return "unknown"
}

func parseProcUptime(b []byte) (int64, error) {
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0, errors.New("empty /proc/uptime")
	}
	v, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return 0, err
	}
	return int64(v), nil
}
