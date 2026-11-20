package server

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
)

// MoneroProvider defines the interface for interacting with a Monero wallet
type MoneroProvider interface {
	CreatePaymentAddress(ctx context.Context, orderID pgtype.UUID, amountSat int64) (address string, paymentID string, err error)
	CheckPayments(ctx context.Context) (map[pgtype.UUID]int64, error)
}

// MockMoneroProvider is a test/stub implementation
type MockMoneroProvider struct{}

func (m *MockMoneroProvider) CreatePaymentAddress(ctx context.Context, orderID pgtype.UUID, amountSat int64) (string, string, error) {
	addr := fmt.Sprintf("4MockXmrAddressForOrder%sxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", uuidString(orderID)[:8])
	paymentID := fmt.Sprintf("mock_pid_%s", uuidString(orderID)[:8])
	return addr, paymentID, nil
}

func (m *MockMoneroProvider) CheckPayments(ctx context.Context) (map[pgtype.UUID]int64, error) {
	return make(map[pgtype.UUID]int64), nil
}
