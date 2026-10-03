package main

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
		total += it.PriceCents * int64(it.Quantity)
	}
	return int64(float64(total) * (1 - c.Discount/100))
}
