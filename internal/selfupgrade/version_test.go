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
		{"0.1.4-beta.1", "0.1.3", true},
		{"0.1.4-beta.2", "0.1.4-beta.1", true},
		{"0.1.4-beta.10", "0.1.4-beta.2", true},
		{"0.1.4", "0.1.4-beta.2", true},
		{"0.1.3", "0.1.4-beta.2", false},
		{"0.1.4-beta.1", "0.1.4", false},
		{"0.1.4-beta.1", "0.1.4-beta.1-3-gabc1234", false},
		{"0.1.4-beta.2", "0.1.4-beta.1-3-gabc1234", true},
		{"0.1.4-beta.1", "0.1.4-beta.1-dirty", false},
		{"0.1.4-beta.1", "0.1.4-alpha.1", true},
		{"0.1.4-beta", "0.1.4-beta.1", false},
		{"0.1.4-beta.1", "dev", true},
		{"0.1.4-beta", "0.1.4-1", true},
		{"0.1.4-1", "0.1.4-beta", false},
		{"0.1.4-beta.1", "0.1.4-beta", true},
	} {
		if got := Newer(tc.latest, tc.current); got != tc.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", tc.latest, tc.current, got, tc.want)
		}
	}
}

func TestIsPrerelease(t *testing.T) {
	for v, want := range map[string]bool{
		"0.1.4-beta.1": true, "0.1.4-beta.1-3-gabc1234": true, "v0.1.4-beta.1": true,
		"0.1.4": false, "0.1.3-14-g0cac2131": false, "0.1.3-dirty": false, "dev": false, "": false,
	} {
		if got := IsPrerelease(v); got != want {
			t.Errorf("IsPrerelease(%q) = %v, want %v", v, got, want)
		}
	}
}
