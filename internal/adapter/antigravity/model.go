package antigravity

import "strings"

var modelFamilyColor = map[string]string{
	"gpt":    "#83a598",
	"gemini": "#8ec07c",
	"claude": "#d3869b",
}

func modelNameColor(id string) (name, color string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", ""
	}
	lower := strings.ToLower(id)
	for family, c := range modelFamilyColor {
		if strings.HasPrefix(lower, family) {
			return id, c
		}
	}
	return id, ""
}
