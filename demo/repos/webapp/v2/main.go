package main

import (
	"fmt"
	"net/http"
)

func productsHandler(w http.ResponseWriter, r *http.Request) {
	category := r.URL.Query().Get("category")
	if category == "" {
		fmt.Fprintf(w, `{"products":[]}`)
		return
	}
	fmt.Fprintf(w, `{"category":%q,"products":[]}`, category)
}

func main() {
	http.HandleFunc("/api/products", productsHandler)
	http.ListenAndServe(":8080", nil)
}
