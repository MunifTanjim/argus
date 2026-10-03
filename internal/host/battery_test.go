package host

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/MunifTanjim/argus/internal/api"
)

func ioregFixture(cur, max, charging, external, full string) []byte {
	return []byte(`+-o AppleSmartBattery  <class AppleSmartBattery, id 0x100000b2b, registered, matched, active, busy 0 (3 ms), retain 8>
    {
      "PostChargeWaitSeconds" = 120
      "built-in" = Yes
      "CurrentCapacity" = ` + cur + `
      "BatteryData" = {"CurrentCapacity"=1,"MaxCapacity"=7}
      "ExternalConnected" = ` + external + `
      "FullyCharged" = ` + full + `
      "MaxCapacity" = ` + max + `
      "IsCharging" = ` + charging + `
    }
    +-o Child  <class Child>
      {
        "CurrentCapacity" = 3
      }
`)
}

func TestParseIORegBattery(t *testing.T) {
	cases := []struct {
		name string
		out  []byte
		want *api.HostBattery
	}{
		{"discharging", ioregFixture("78", "100", "No", "No", "No"), &api.HostBattery{Percent: 78, State: "discharging"}},
		{"charging", ioregFixture("50", "100", "Yes", "Yes", "No"), &api.HostBattery{Percent: 50, State: "charging"}},
		{"full", ioregFixture("100", "100", "No", "Yes", "Yes"), &api.HostBattery{Percent: 100, State: "full"}},
		{"not charging", ioregFixture("80", "100", "No", "Yes", "No"), &api.HostBattery{Percent: 80, State: "not_charging"}},
		{"intel mAh", ioregFixture("4000", "5000", "No", "No", "No"), &api.HostBattery{Percent: 80, State: "discharging"}},
		{"clamped", ioregFixture("5200", "5000", "No", "No", "No"), &api.HostBattery{Percent: 100, State: "discharging"}},
		{"no battery", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseIORegBattery(tc.out)
			if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func writeSupply(t *testing.T, root, name string, attrs map[string]string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for k, v := range attrs {
		if err := os.WriteFile(filepath.Join(dir, k), []byte(v+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReadSysfsBattery(t *testing.T) {
	root := t.TempDir()
	writeSupply(t, root, "AC", map[string]string{"type": "Mains", "online": "1"})
	writeSupply(t, root, "BAT0", map[string]string{"type": "Battery", "capacity": "64", "status": "Not charging"})
	writeSupply(t, root, "AAA-mouse", map[string]string{"type": "Battery", "scope": "Device", "capacity": "10", "status": "Discharging"})
	got, err := readSysfsBattery(root)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || *got != (api.HostBattery{Percent: 64, State: "not_charging"}) {
		t.Fatalf("got %+v", got)
	}
}

func TestReadSysfsBatteryNone(t *testing.T) {
	root := t.TempDir()
	writeSupply(t, root, "AC", map[string]string{"type": "Mains"})
	if got, err := readSysfsBattery(root); err != nil || got != nil {
		t.Fatalf("got %+v, %v", got, err)
	}
	if got, err := readSysfsBattery(filepath.Join(root, "missing")); err != nil || got != nil {
		t.Fatalf("missing root: got %+v, %v", got, err)
	}
}

func TestSysfsState(t *testing.T) {
	for in, want := range map[string]string{"Charging": "charging", "Discharging": "discharging", "Full": "full", "Not charging": "not_charging", "Unknown": "unknown", "": "unknown"} {
		if got := sysfsState(in); got != want {
			t.Errorf("sysfsState(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseProcUptime(t *testing.T) {
	got, err := parseProcUptime([]byte("350735.47 234388.90\n"))
	if err != nil || got != 350735 {
		t.Fatalf("got %d, %v", got, err)
	}
	if _, err := parseProcUptime(nil); err == nil {
		t.Fatal("empty input must fail")
	}
}

func TestUptimePositive(t *testing.T) {
	got, err := Uptime()
	if err != nil {
		t.Fatal(err)
	}
	if got <= 0 {
		if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
			t.Fatalf("uptime %d, want > 0", got)
		}
		t.Skip("platform without uptime support")
	}
}
