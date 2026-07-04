package claudecode

import (
	"regexp"
	"strings"
)

// Family display colors (gruvbox).
var modelFamilyColor = map[string]string{
	"fable":  "#fe8019",
	"opus":   "#d3869b",
	"sonnet": "#83a598",
	"haiku":  "#b8bb26",
}

var digitsRe = regexp.MustCompile(`^\d+$`)

// modelDisplayName pretty-prints a Claude model id (e.g. "claude-opus-4-8" -> "Opus 4.8").
func modelDisplayName(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return raw
	}

	variant := ""
	if i := strings.IndexByte(s, '['); i >= 0 {
		variant = s[i:]
		s = s[:i]
	}

	if !strings.HasPrefix(s, "claude-") {
		return raw
	}

	family := ""
	var version []string
	for _, part := range strings.Split(strings.TrimPrefix(s, "claude-"), "-") {
		if _, ok := modelFamilyColor[strings.ToLower(part)]; ok {
			family = strings.ToLower(part)
		} else if len(part) < 6 && digitsRe.MatchString(part) {
			version = append(version, part) // skip date stamps (>= 6 digits)
		}
	}
	if family == "" {
		return raw
	}

	label := strings.ToUpper(family[:1]) + family[1:]
	if len(version) > 0 {
		label += " " + strings.Join(version, ".")
	}
	if variant != "" {
		label += " " + variant
	}
	return label
}

func modelColorHex(raw string) string {
	m := strings.ToLower(raw)
	for family, hex := range modelFamilyColor {
		if strings.Contains(m, family) {
			return hex
		}
	}
	return ""
}
