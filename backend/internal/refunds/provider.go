package refunds

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"
)

// DemoProvider stores accepted outcomes independently from business refunds.
type DemoProvider struct {
	DB      *sql.DB
	Channel string
}

func NewDemoProvider(db *sql.DB, channel string) *DemoProvider {
	return &DemoProvider{DB: db, Channel: channel}
}

func (p *DemoProvider) SubmitRefund(ctx context.Context, request ProviderRequest) (ProviderResult, error) {
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return ProviderResult{}, err
	}
	defer tx.Rollback()

	var orderID string
	var amount int64
	var result ProviderResult
	created := false
	err = tx.QueryRowContext(ctx, `SELECT order_id, amount_cents, status, reason FROM provider_records WHERE provider_channel = ? AND provider_request_id = ?`, p.Channel, request.RequestID).Scan(&orderID, &amount, &result.Status, &result.Reason)
	switch {
	case err == nil:
		if orderID != request.OrderID || amount != request.Amount {
			return ProviderResult{}, errors.New("provider request id reused with different parameters")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE provider_records SET submit_calls = submit_calls + 1 WHERE provider_channel = ? AND provider_request_id = ?`, p.Channel, request.RequestID); err != nil {
			return ProviderResult{}, err
		}
	case errors.Is(err, sql.ErrNoRows):
		created = true
		result = ProviderResult{RequestID: request.RequestID, Status: StatusSucceeded}
		if p.Channel != "mock-success" && p.Channel != "mock-unknown" && p.Channel != "mock-fail" && p.Channel != "mock-a" && p.Channel != "mock-b" {
			return ProviderResult{}, fmt.Errorf("unknown demo channel %q", p.Channel)
		}
		if p.Channel == "mock-fail" {
			result.Status, result.Reason = StatusFailed, "Payment channel rejected the refund. Contact support before retrying."
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO provider_records (provider_channel, provider_request_id, order_id, amount_cents, status, reason, submit_calls, created_at) VALUES (?, ?, ?, ?, ?, ?, 1, ?)`, p.Channel, request.RequestID, request.OrderID, request.Amount, result.Status, result.Reason, time.Now().UTC().Format(time.RFC3339Nano))
		if err != nil {
			return ProviderResult{}, err
		}
	default:
		return ProviderResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return ProviderResult{}, err
	}
	result.RequestID = request.RequestID
	var accepted int
	if err := p.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM provider_records WHERE provider_channel = ?`, p.Channel).Scan(&accepted); err != nil {
		return ProviderResult{}, err
	}
	var attempts int
	_ = p.DB.QueryRowContext(ctx, `SELECT submit_calls FROM provider_records WHERE provider_channel = ? AND provider_request_id = ?`, p.Channel, request.RequestID).Scan(&attempts)
	log.Printf("demo provider channel=%s request_id=%s submit_calls=%d accepted_records=%d", p.Channel, request.RequestID, attempts, accepted)
	if created && p.Channel == "mock-unknown" {
		return ProviderResult{Status: StatusUnknown, RequestID: request.RequestID, Reason: "Channel response was lost; query the original refund request."}, errors.New("simulated timeout after channel acceptance")
	}
	return result, nil
}

func (p *DemoProvider) QueryRefund(ctx context.Context, requestID string) (ProviderResult, error) {
	var result ProviderResult
	err := p.DB.QueryRowContext(ctx, `SELECT status, reason FROM provider_records WHERE provider_channel = ? AND provider_request_id = ?`, p.Channel, requestID).Scan(&result.Status, &result.Reason)
	result.RequestID = requestID
	if errors.Is(err, sql.ErrNoRows) {
		return ProviderResult{Status: StatusUnknown, RequestID: requestID, Reason: "No accepted channel result is available yet."}, errors.New("provider request not found")
	}
	if err != nil {
		return ProviderResult{}, err
	}
	return result, nil
}
