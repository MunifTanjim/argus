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
		line := it.PriceCents * int64(it.Quantity)
		total += int64(math.Round(float64(line) * (1 - c.Discount/100)))
	}
	return total
}
