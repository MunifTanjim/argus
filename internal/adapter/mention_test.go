package adapter

import (
	"reflect"
	"testing"
)

func TestMentions(t *testing.T) {
	cases := []struct {
		text string
		want []Mention
	}{
		{"$a", []Mention{{At: 0, Name: "a"}}},
		{"run $a-b_c:d now", []Mention{{At: 4, Name: "a-b_c:d"}}},
		{"x\n$a", []Mention{{At: 2, Name: "a"}}},
		{"é $añ.", []Mention{{At: 3, Name: "añ"}}},
		{"$a $a", []Mention{{At: 0, Name: "a"}, {At: 3, Name: "a"}}},
		{"x$a", nil},
		{"$5 $_a $", nil},
	}
	for _, c := range cases {
		if got := Mentions(c.text, '$'); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Mentions(%q) = %+v, want %+v", c.text, got, c.want)
		}
	}
}

func TestIsMentionName(t *testing.T) {
	for name, want := range map[string]bool{
		"tdd":     true,
		"a-b_c:d": true,
		"añ2":     true,
		"":        false,
		"2a":      false,
		"v1.2":    false,
		"a b":     false,
	} {
		if got := IsMentionName(name); got != want {
			t.Errorf("IsMentionName(%q) = %v, want %v", name, got, want)
		}
	}
}
