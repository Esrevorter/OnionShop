package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// CartItem represents a product and quantity in the shopping cart.
type CartItem struct {
	ProductID string `json:"product_id"`
	Title     string `json:"title"`
	PriceSat  int64  `json:"price_sat"`
	Qty       int    `json:"qty"`
	VendorID  string `json:"vendor_id"`
}

// Cart holds items keyed by product id.
type Cart map[string]CartItem

const cartKeyPrefix = "cart:"

// GetCart retrieves the cart from Redis for the current session token or user ID.
func GetCart(ctx context.Context, rdb *redis.Client, identity string) (Cart, error) {
	if rdb == nil {
		return make(Cart), nil
	}
	data, err := rdb.Get(ctx, cartKeyPrefix+identity).Bytes()
	if errors.Is(err, redis.Nil) {
		return make(Cart), nil
	}
	if err != nil {
		return nil, fmt.Errorf("server: get cart: %w", err)
	}
	var cart Cart
	if err := json.Unmarshal(data, &cart); err != nil {
		return make(Cart), nil
	}
	return cart, nil
}

// SaveCart persists the cart to Redis with an idle TTL (7 days).
func SaveCart(ctx context.Context, rdb *redis.Client, identity string, cart Cart) error {
	if rdb == nil || identity == "" {
		return nil
	}
	data, err := json.Marshal(cart)
	if err != nil {
		return fmt.Errorf("server: marshal cart: %w", err)
	}
	return rdb.Set(ctx, cartKeyPrefix+identity, data, 7*24*time.Hour).Err()
}
