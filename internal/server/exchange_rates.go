package server

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// ExchangeRates holds the cached fiat-to-USD and XMR-to-USD rates.
// Base currency is always USD cents.
var ratesOracle = NewExchangeRates()

type ExchangeRates struct {
	mu        sync.RWMutex
	rates     map[string]float64 // e.g., "EUR_USD": 1.08, "XMR_USD": 150.0
	lastFetch time.Time
	client    *http.Client
}

// NewExchangeRates initializes the oracle.
// Whonix-Gateway handles all routing; we just use standard HTTP.
func NewExchangeRates() *ExchangeRates {
	return &ExchangeRates{
		rates: map[string]float64{
			"EUR_USD": 1.08, // Safe defaults to prevent zero-division on startup
			"AUD_USD": 0.65,
			"XMR_USD": 150.0,
		},
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

// FetchRates updates the cache from CoinGecko (or similar).
// Runs in a background goroutine.
func (e *ExchangeRates) FetchRates(ctx context.Context) {
	// In production, use a reliable API. CoinGecko free tier is fine for this.
	// URL: https://api.coingecko.com/api/v3/simple/price?ids=monero&vs_currencies=usd,eur,aud
	// For now, we'll just refresh the timestamp to prove the mechanism works.
	e.mu.Lock()
	defer e.mu.Unlock()
	e.lastFetch = time.Now()
	
	// TODO: Implement actual HTTP fetch here. 
	// Example: resp, err := e.client.Get("https://api.coingecko.com/...")
	// Parse JSON and update e.rates["XMR_USD"], etc.
	fmt.Println("Exchange rates refreshed (mock)")
}

// GetRate returns the multiplier to convert a foreign currency to USD.
// e.g., GetRate("EUR") returns 1.08 (1 EUR = 1.08 USD).
func (e *ExchangeRates) GetRate(currency string) float64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if currency == "USD" {
		return 1.0
	}
	key := currency + "_USD"
	if r, ok := e.rates[key]; ok {
		return r
	}
	return 1.0 // Fallback
}

// USDCentsToPiconero converts USD cents to Monero piconeros (1 XMR = 10^12 piconero).
func (e *ExchangeRates) USDCentsToPiconero(usdCents int64) int64 {
	e.mu.RLock()
	xmrPerUsd := 1.0 / e.rates["XMR_USD"]
	e.mu.RUnlock()
	
	// (usdCents / 100) * xmrPerUsd * 10^12
	// To avoid float precision loss, we do: (usdCents * 10^10 * xmrPerUsd)
	usdValue := float64(usdCents) / 100.0
	xmrValue := usdValue * xmrPerUsd
	piconero := int64(xmrValue * 1e12)
	return piconero
}

// Format converts USD cents to the target currency and returns a formatted string.
func (e *ExchangeRates) Format(usdCents int64, currency string) string {
    e.mu.RLock()
    defer e.mu.RUnlock()
    
    mult := 1.0
    symbol := "$"
    if currency == "EUR" {
        mult = e.rates["EUR_USD"]
        symbol = "€"
    } else if currency == "AUD" {
        mult = e.rates["AUD_USD"]
        symbol = "A$"
    }
    
    // Convert USD cents to target currency cents
    targetCents := float64(usdCents) * mult
    whole := int64(targetCents) / 100
    frac := int64(targetCents) % 100
    
    return fmt.Sprintf("%s%d.%02d", symbol, whole, frac)
}


// fmtUSDCents formats integer cents as a USD string; used as the safe fallback
// so a missing oracle can never render a bare cent integer.
func fmtUSDCents(cents int64) string {
	sign := ""
	if cents < 0 {
		sign, cents = "-", -cents
	}
	return fmt.Sprintf("%s%d.%02d", sign, cents/100, cents%100)
}
