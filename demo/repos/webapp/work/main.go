package main

import (
	"encoding/json"
	"net/http"
)

type Product struct {
	ID       int    `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
}

var products = []Product{
	{ID: 1, Name: "Widget A", Category: "widgets"},
	{ID: 2, Name: "Widget B", Category: "widgets"},
	{ID: 3, Name: "Gadget X", Category: "gadgets"},
}

func productsHandler(w http.ResponseWriter, r *http.Request) {
	category := r.URL.Query().Get("category")
	var out []Product
	for _, p := range products {
		if category == "" || p.Category == category {
			out = append(out, p)
		}
	}
	if out == nil {
		out = []Product{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func main() {
	http.HandleFunc("/api/products", productsHandler)
	http.ListenAndServe(":8080", nil)
}
