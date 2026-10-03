package main

type LineItem struct {
	Name     string
	Price    float64
	Quantity int
}

type Cart struct {
	Items    []LineItem
	Discount float64 // percent, 0-100
}

func (c Cart) Total() float64 {
	var total float64
	for _, it := range c.Items {
		total += it.Price * float64(it.Quantity)
	}
	return total * (1 - c.Discount/100)
}
