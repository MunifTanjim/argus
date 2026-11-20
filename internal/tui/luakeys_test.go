package tui

import (
	"slices"
	"strings"
	"testing"
)

func TestParseRexKeySeq(t *testing.T) {
	for in, want := range map[string]keySeq{
		"ctrl+h":              {{"ctrl+h"}},
		"Ctrl+H":              {{"ctrl+h"}},
		"control+h":           {{"ctrl+h"}},
		"d":                   {{"d"}},
		"D":                   {{"D", "shift+d"}},
		"shift+d":             {{"D", "shift+d"}},
		"opt+x":               {{"alt+x"}},
		"shift+cmd+d":         {{"super+D", "shift+super+d"}},
		"meta+x":              {{"super+x"}},
		"ctrl+alt+shift+left": {{"ctrl+alt+shift+left"}},
		"escape":              {{"esc"}},
		"PageUp":              {{"pgup"}},
		"delete":              {{"delete"}},
		"F12":                 {{"f12"}},
		"&":                   {{"&"}},
		"alt+&":               {{"alt+&"}},
		":":                   {{":"}},
		"ctrl+:":              {{"ctrl+:"}},
		"+":                   {{"+"}},
		"alt++":               {{"alt++"}},
		">":                   {{">"}},
		"alt+>":               {{"alt+>"}},
		"ctrl+a>c":            {{"ctrl+a"}, {"c"}},
		"ctrl+a>+":            {{"ctrl+a"}, {"+"}},
		"ctrl+a>>":            {{"ctrl+a"}, {">"}},
		">>x":                 {{">"}, {"x"}},
		"\\":                  {{"\\"}},
	} {
		got, err := parseRexKeySeq(in)
		if err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if !slices.EqualFunc(got, want, slices.Equal) {
			t.Errorf("%s = %q, want %q", in, got, want)
		}
	}
}

func TestParseRexKeySeqErrors(t *testing.T) {
	for in, want := range map[string]string{
		"":            `unknown key ""`,
		"ctrl+a>":     `"ctrl+a>": unknown key ""`,
		"foo":         `unknown key "foo"`,
		"hyper+x":     `unknown key "hyper+x"`,
		"ctrl+":       `unknown key "ctrl+"`,
		"++":          `unknown key "++"`,
		"+a":          `unknown key "+a"`,
		"ctrl+[KeyA]": "physical keys are not supported",
		"f13":         `unknown key "f13"`,
		"esc":         `unknown key "esc"`,
	} {
		_, err := parseRexKeySeq(in)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want %q", in, err, want)
		}
	}
}
