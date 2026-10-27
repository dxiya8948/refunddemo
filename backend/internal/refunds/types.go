package refunds

import "context"

type Status string

const (
	StatusProcessing Status = "PROCESSING"
	StatusUnknown    Status = "UNKNOWN"
	StatusSucceeded  Status = "SUCCEEDED"
	StatusFailed     Status = "FAILED"
)

type Role string

const (
	RoleMerchant Role = "merchant"
	RoleReadOnly Role = "read_only"
)

type Principal struct {
	MerchantID string `json:"merchant_id"`
	Role       Role   `json:"role"`
}

type Order struct {
	ID                   string `json:"id"`
	MerchantID           string `json:"merchant_id"`
	AmountCents          int64  `json:"amount_cents"`
	Currency             string `json:"currency"`
	Status               string `json:"status"`
	ProviderChannel      string `json:"provider_channel"`
	PaymentChannelStatus string `json:"payment_channel_status"`
	RefundID             string `json:"refund_id,omitempty"`
	RefundStatus         Status `json:"refund_status,omitempty"`
	RefundReason         string `json:"refund_reason,omitempty"`
	RefundMessage        string `json:"refund_message,omitempty"`
	RefundUpdatedAt      string `json:"refund_updated_at,omitempty"`
	ProviderRequestID    string `json:"provider_request_id,omitempty"`
}

type Refund struct {
	ID                string `json:"id"`
	OrderID           string `json:"order_id"`
	MerchantID        string `json:"merchant_id"`
	AmountCents       int64  `json:"amount_cents"`
	Currency          string `json:"currency"`
	Status            Status `json:"status"`
	Reason            string `json:"reason"`
	Message           string `json:"message"`
	IdempotencyKey    string `json:"idempotency_key"`
	ProviderChannel   string `json:"provider_channel"`
	ProviderRequestID string `json:"provider_request_id"`
	FailureReason     string `json:"failure_reason,omitempty"`
	CreatedAt         string `json:"created_at"`
	UpdatedAt         string `json:"updated_at"`
}

type ProviderRequest struct {
	RequestID string
	OrderID   string
	Amount    int64
	Currency  string
}

type ProviderResult struct {
	Status    Status
	RequestID string
	Reason    string
}

type Provider interface {
	SubmitRefund(context.Context, ProviderRequest) (ProviderResult, error)
	QueryRefund(context.Context, string) (ProviderResult, error)
}

type PaymentProvider interface {
	QueryPayment(context.Context, string) (string, error)
}
