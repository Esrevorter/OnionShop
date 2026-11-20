package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// MoneroProvider defines the interface for interacting with a Monero wallet.
type MoneroProvider interface {
	CreatePaymentAddress(ctx context.Context, orderID pgtype.UUID) (address string, paymentID string, err error)
	CheckPayments(ctx context.Context, pendingOrders map[pgtype.UUID]string) (map[pgtype.UUID]int64, error)
}

// RealMoneroProvider implements MoneroProvider using monero-wallet-rpc.
type RealMoneroProvider struct {
	rpcURL   string
	username string
	password string
	client   *http.Client
}

func NewRealMoneroProvider(rpcURL, username, password string) *RealMoneroProvider {
	return &RealMoneroProvider{
		rpcURL:   rpcURL,
		username: username,
		password: password,
		client:   &http.Client{Timeout: 10 * time.Second},
	}
}

type rpcRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      string      `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
}

func (p *RealMoneroProvider) doRPC(ctx context.Context, method string, params interface{}, result interface{}) error {
	reqBody := rpcRequest{JSONRPC: "2.0", ID: "1", Method: method, Params: params}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", p.rpcURL, bytes.NewBuffer(body))
	if err != nil {
		return err
	}
	req.SetBasicAuth(p.username, p.password)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("rpc request failed with status: %d", resp.StatusCode)
	}
	var rpcResp struct {
		Result interface{} `json:"result"`
		Error  interface{} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return err
	}
	if rpcResp.Error != nil {
		return fmt.Errorf("rpc error: %v", rpcResp.Error)
	}
	resBytes, err := json.Marshal(rpcResp.Result)
	if err != nil {
		return err
	}
	return json.Unmarshal(resBytes, result)
}

func (p *RealMoneroProvider) CreatePaymentAddress(ctx context.Context, orderID pgtype.UUID) (string, string, error) {
	label := "order-" + uuidString(orderID)
	params := map[string]interface{}{"account_index": 0, "label": label}
	var result struct {
		Address      string `json:"address"`
		AddressIndex uint64 `json:"address_index"`
	}
	if err := p.doRPC(ctx, "create_address", params, &result); err != nil {
		return "", "", fmt.Errorf("create_address failed: %w", err)
	}
	return result.Address, fmt.Sprintf("%d", result.AddressIndex), nil
}

func (p *RealMoneroProvider) CheckPayments(ctx context.Context, pendingOrders map[pgtype.UUID]string) (map[pgtype.UUID]int64, error) {
	params := map[string]interface{}{"in": true, "pool": false}
	var result struct {
		In []struct {
			Address string `json:"address"`
			Amount  uint64 `json:"amount"`
		} `json:"in"`
	}
	if err := p.doRPC(ctx, "get_transfers", params, &result); err != nil {
		return nil, fmt.Errorf("get_transfers failed: %w", err)
	}
	payments := make(map[pgtype.UUID]int64)
	for _, transfer := range result.In {
		for orderID, addr := range pendingOrders {
			if strings.TrimSpace(transfer.Address) == strings.TrimSpace(addr) {
				satoshis := int64(transfer.Amount / 10000)
				payments[orderID] += satoshis
			}
		}
	}
	return payments, nil
}

// MockMoneroProvider for testing
type MockMoneroProvider struct{}

func (m *MockMoneroProvider) CreatePaymentAddress(ctx context.Context, orderID pgtype.UUID) (string, string, error) {
	addr := fmt.Sprintf("4MockXmrAddressForOrder%s", uuidString(orderID)[:8])
	return addr, "mock_pid", nil
}

func (m *MockMoneroProvider) CheckPayments(ctx context.Context, pendingOrders map[pgtype.UUID]string) (map[pgtype.UUID]int64, error) {
	return make(map[pgtype.UUID]int64), nil
}

// NewMoneroProvider factory
func NewMoneroProvider() MoneroProvider {
	rpcURL := os.Getenv("MONERO_WALLET_RPC_URL")
	if rpcURL == "" {
		return &MockMoneroProvider{}
	}
	return NewRealMoneroProvider(rpcURL, "monero", os.Getenv("XMR_WALLET_RPC_PASSWORD"))
}
