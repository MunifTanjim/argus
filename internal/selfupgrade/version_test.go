package selfupgrade

import "testing"

func TestNewer(t *testing.T) {
	for _, tc := range []struct {
		latest, current string
		want            bool
	}{
		{"0.1.3", "0.1.3", false},
		{"0.1.4", "0.1.3", true},
		{"0.2.0", "0.1.10", true},
		{"0.1.3", "0.1.4", false},
		{"0.1.3", "0.1.3-14-g0cac2131", false},
		{"0.1.3", "0.1.3-14-g0cac2131-dirty", false},
		{"0.1.3", "0.1.3-dirty", false},
		{"0.1.4", "0.1.3-14-g0cac2131", true},
		{"v0.1.3", "0.1.3", false},
		{"0.1.3", "dev", true},
		{"0.1.3", "0cac2131", true},
		{"", "0.1.3", false},
		{"", "dev", false},
	} {
		if got := Newer(tc.latest, tc.current); got != tc.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", tc.latest, tc.current, got, tc.want)
		}
	}
}
