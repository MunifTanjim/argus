package main

import "math"

type LineItem struct {
	Name       string
	PriceCents int64
	Quantity   int
}

type Cart struct {
	Items    []LineItem
	Discount float64 // percent, 0-100
}

func (c Cart) TotalCents() int64 {
	var total int64
	for _, it := range c.Items {
		total += discounted(it.PriceCents*int64(it.Quantity), c.Discount)
	}
	return total
}

func discounted(cents int64, percent float64) int64 {
	return int64(math.Round(float64(cents) * (1 - percent/100)))
}
