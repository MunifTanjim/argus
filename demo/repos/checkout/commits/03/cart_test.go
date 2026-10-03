package main

import "testing"

func TestTotalCentsMultiItem(t *testing.T) {
	c := Cart{
		Items: []LineItem{
			{Name: "Widget A", PriceCents: 1999, Quantity: 3},
			{Name: "Gadget X", PriceCents: 4995, Quantity: 1},
		},
		Discount: 15,
	}
	if got := c.TotalCents(); got != 9343 {
		t.Fatalf("TotalCents = %d, want 9343", got)
	}
}
