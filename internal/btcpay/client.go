package btcpay

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Client communicates with the BTCPay Server REST API.
type Client struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

// Invoice represents a BTCPay Server invoice status response.
type Invoice struct {
	ID           string `json:"id"`
	Status       string `json:"status"` // New, Processing, Settled, Expired, Invalid
	CheckoutLink string `json:"checkoutLink"`
	Amount       string `json:"amount"`
	Currency     string `json:"currency"`
}

// NewClient returns a new BTCPay API client.
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		client:  &http.Client{Timeout: 15 * time.Second},
	}
}

// GetInvoice fetches the latest status of an invoice by ID.
func (c *Client) GetInvoice(ctx context.Context, storeID, invoiceID string) (*Invoice, error) {
	url := fmt.Sprintf("%s/api/v1/stores/%s/invoices/%s", c.baseURL, storeID, invoiceID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("btcpay: new request: %w", err)
	}
	req.Header.Set("Authorization", "token "+c.apiKey)
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("btcpay: do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("btcpay: unexpected status %d", resp.StatusCode)
	}

	var inv Invoice
	if err := json.NewDecoder(resp.Body).Decode(&inv); err != nil {
		return nil, fmt.Errorf("btcpay: decode: %w", err)
	}
	return &inv, nil
}
