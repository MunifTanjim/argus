package tui

// dividerWidth is the rendered width of the inter-column divider (" │ ").
const dividerWidth = 3

func clampWidth(w, minW, maxW int) int {
	if maxW < minW {
		maxW = minW
	}
	if w < minW {
		return minW
	}
	if w > maxW {
		return maxW
	}
	return w
}
