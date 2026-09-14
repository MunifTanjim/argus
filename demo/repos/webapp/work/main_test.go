package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProductsEmptyCategory(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/products?category=", nil)
	w := httptest.NewRecorder()
	productsHandler(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
}
