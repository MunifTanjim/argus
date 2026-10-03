# web-app

A small product catalog and checkout API.

## Endpoints

- `GET /api/products` lists products.
- `POST /api/checkout` returns the cart total in cents.

Prices are integer cents. A discount is applied to each line and rounded
half-up, so a cart total never drifts by a cent.
