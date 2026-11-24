package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// MoneroProvider defines the interface for XMR operations.
type MoneroProvider interface {
	CreatePaymentAddress(ctx context.Context, orderID pgtype.UUID) (addr, paymentID string, err error)
	CheckPayment(ctx context.Context, orderID pgtype.UUID, address string, expectedPiconero int64) (paid bool, txHash string, err error)
	ObservedAmount(ctx context.Context, orderID pgtype.UUID, address string) (amount int64, txHash string, confirmations int, found bool, err error)
	GetBalance(ctx context.Context) (int64, error)
	SendTransaction(ctx context.Context, destAddr string, piconero int64) (txHash string, err error)
}

// RPCProvider implements MoneroProvider using monero-wallet-rpc JSON-RPC.
// It self-heals: if wallet-rpc has no wallet open (e.g. after a container
// restart in --wallet-dir mode), the failing call re-opens the configured
// wallet once and retries.
type RPCProvider struct {
	rpcURL        string
	rpcUser       string
	rpcPass       string
	walletName    string
	walletPass    string
	confirmations int
	client        *http.Client
}

func NewRPCProvider() *RPCProvider {
	get := func(key, def string) string {
		if v := os.Getenv(key); v != "" {
			return v
		}
		return def
	}
	confirmations := 3
	if c := os.Getenv("XMR_CONFIRMATIONS_REQUIRED"); c != "" {
		if n, err := strconv.Atoi(c); err == nil && n > 0 {
			confirmations = n
		}
	}
	return &RPCProvider{
		rpcURL:        get("XMR_WALLET_RPC_URL", "http://monero-wallet-rpc:18083/json_rpc"),
		rpcUser:       get("XMR_WALLET_RPC_USER", ""),
		rpcPass:       get("XMR_WALLET_RPC_PASSWORD", ""),
		walletName:    get("XMR_WALLET_NAME", "onionshop"),
		walletPass:    get("XMR_WALLET_PASSWORD", "testpass123"),
		confirmations: confirmations,
		client:        &http.Client{Timeout: 10 * time.Second},
	}
}

type jsonRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      string      `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      string          `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *jsonRPCError   `json:"error"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (p *RPCProvider) doRPC(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	body, err := json.Marshal(jsonRPCRequest{JSONRPC: "2.0", ID: "0", Method: method, Params: params})
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.rpcURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if p.rpcUser != "" || p.rpcPass != "" {
		httpReq.SetBasicAuth(p.rpcUser, p.rpcPass)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("rpc call failed: %w", err)
	}
	defer resp.Body.Close()
	var rpcResp jsonRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("rpc error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	return rpcResp.Result, nil
}

func isNoWalletErr(err error) bool {
	if err == nil {
		return false
	}
	m := strings.ToLower(err.Error())
	return strings.Contains(m, "no wallet") || strings.Contains(m, "not open") || strings.Contains(m, "must be open")
}

func (p *RPCProvider) openDefaultWallet(ctx context.Context) error {
	_, err := p.doRPC(ctx, "open_wallet", map[string]interface{}{
		"filename": p.walletName, "password": p.walletPass,
	})
	if err != nil {
		log.Printf("xmr: auto open_wallet %q failed: %v", p.walletName, err)
	}
	return err
}

// callRPC wraps doRPC with a single self-heal retry when no wallet is open.
func (p *RPCProvider) callRPC(ctx context.Context, method string, params interface{}) (json.RawMessage, error) {
	res, err := p.doRPC(ctx, method, params)
	if err != nil && method != "open_wallet" && isNoWalletErr(err) {
		log.Printf("xmr: no wallet open; auto-opening %q and retrying %s", p.walletName, method)
		if oerr := p.openDefaultWallet(ctx); oerr == nil {
			return p.doRPC(ctx, method, params)
		}
	}
	return res, err
}

func (p *RPCProvider) ObservedAmount(ctx context.Context, orderID pgtype.UUID, address string) (int64, string, int, bool, error) {
	result, err := p.callRPC(ctx, "get_transfers", map[string]interface{}{"in": true, "account_index": 0, "filter_by_height": false})
	if err != nil { return 0, "", 0, false, err }
	var resp struct { In []struct {
		TxID string `json:"txid"`; Amount int64 `json:"amount"`; Confirmations int `json:"confirmations"`; Address string `json:"address"`
	} `json:"in"` }
	if err := json.Unmarshal(result, &resp); err != nil { return 0, "", 0, false, err }
	for _, t := range resp.In {
		if t.Address == address { return t.Amount, t.TxID, t.Confirmations, true, nil }
	}
	return 0, "", 0, false, nil
}

func (p *RPCProvider) CreatePaymentAddress(ctx context.Context, orderID pgtype.UUID) (string, string, error) {
	result, err := p.callRPC(ctx, "create_address", map[string]interface{}{
		"account_index": 0,
		"label":         "order-" + uuidString(orderID)[:8],
	})
	if err != nil {
		return "", "", fmt.Errorf("create_address: %w", err)
	}
	var resp struct {
		Address      string `json:"address"`
		AddressIndex int    `json:"address_index"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return "", "", fmt.Errorf("unmarshal response: %w", err)
	}
	return resp.Address, uuidString(orderID), nil
}

func (p *RPCProvider) CheckPayment(ctx context.Context, orderID pgtype.UUID, address string, expectedPiconero int64) (bool, string, error) {
	result, err := p.callRPC(ctx, "get_transfers", map[string]interface{}{
		"in":               true,
		"account_index":    0,
		"filter_by_height": false,
	})
	if err != nil {
		return false, "", fmt.Errorf("get_transfers: %w", err)
	}
	var resp struct {
		In []struct {
			TxID          string `json:"txid"`
			Amount        int64  `json:"amount"`
			Confirmations int    `json:"confirmations"`
			Address       string `json:"address"`
		} `json:"in"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return false, "", fmt.Errorf("unmarshal transfers: %w", err)
	}
	for _, t := range resp.In {
		if t.Address == address && t.Amount >= expectedPiconero && t.Confirmations >= p.confirmations {
			return true, t.TxID, nil
		}
	}
	return false, "", nil
}

func (p *RPCProvider) GetBalance(ctx context.Context) (int64, error) {
	result, err := p.callRPC(ctx, "get_balance", map[string]interface{}{"account_index": 0})
	if err != nil {
		return 0, fmt.Errorf("get_balance: %w", err)
	}
	var resp struct {
		UnlockedBalance int64 `json:"unlocked_balance"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return 0, fmt.Errorf("unmarshal balance: %w", err)
	}
	return resp.UnlockedBalance, nil
}

func (p *RPCProvider) SendTransaction(ctx context.Context, destAddr string, piconero int64) (string, error) {
	result, err := p.callRPC(ctx, "transfer", map[string]interface{}{
		"destinations": []map[string]interface{}{{"address": destAddr, "amount": piconero}},
		"priority":     0,
		"get_tx_key":   true,
	})
	if err != nil {
		return "", fmt.Errorf("transfer: %w", err)
	}
	var resp struct {
		TxHash string `json:"tx_hash"`
	}
	if err := json.Unmarshal(result, &resp); err != nil {
		return "", fmt.Errorf("unmarshal transfer response: %w", err)
	}
	return resp.TxHash, nil
}

// MockProvider for testing/fallback.
type MockProvider struct{}

func (m *MockProvider) ObservedAmount(ctx context.Context, orderID pgtype.UUID, address string) (int64, string, int, bool, error) {
	return 0, "", 0, false, nil
}

func (m *MockProvider) CreatePaymentAddress(ctx context.Context, orderID pgtype.UUID) (string, string, error) {
	return "4" + strings.Repeat("0", 94), uuidString(orderID), nil
}
func (m *MockProvider) CheckPayment(ctx context.Context, orderID pgtype.UUID, address string, expectedPiconero int64) (bool, string, error) {
	return false, "", nil
}
func (m *MockProvider) GetBalance(ctx context.Context) (int64, error) { return 0, nil }
func (m *MockProvider) SendTransaction(ctx context.Context, destAddr string, piconero int64) (string, error) {
	return "mock_tx_hash", nil
}

// NewMoneroProvider factory: returns RPC or Mock based on XMR_MODE env var.
func NewMoneroProvider() MoneroProvider {
	if os.Getenv("XMR_MODE") == "rpc" {
		return NewRPCProvider()
	}
	return &MockProvider{}
}
