package refunds

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Handler struct {
	db        *sql.DB
	providers map[string]Provider
	tokens    map[string]Principal
}

func NewHandler(db *sql.DB, providers map[string]Provider, tokens map[string]Principal) http.Handler {
	h := &Handler{db: db, providers: providers, tokens: tokens}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		h.health(w, r)
	})
	mux.HandleFunc("/api/", h.apiRoute)
	return cors(mux)
}

func (h *Handler) apiRoute(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) == 2 && parts[1] == "orders" {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		h.withAuth(h.listOrders)(w, r)
		return
	}
	if len(parts) == 3 && parts[1] == "orders" {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		orderID := parts[2]
		h.withAuth(func(w http.ResponseWriter, r *http.Request, p Principal) { h.getOrder(w, r, p, orderID) })(w, r)
		return
	}
	if len(parts) == 4 && parts[1] == "orders" && parts[3] == "sync-payment" {
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		orderID := parts[2]
		h.withAuth(func(w http.ResponseWriter, r *http.Request, p Principal) { h.syncPayment(w, r, p, orderID) })(w, r)
		return
	}
	if len(parts) == 4 && parts[1] == "orders" && parts[3] == "refunds" {
		orderID := parts[2]
		switch r.Method {
		case http.MethodGet:
			h.withAuth(func(w http.ResponseWriter, r *http.Request, p Principal) { h.getOrderRefund(w, r, p, orderID) })(w, r)
		case http.MethodPost:
			h.withAuth(func(w http.ResponseWriter, r *http.Request, p Principal) { h.createRefund(w, r, p, orderID) })(w, r)
		default:
			methodNotAllowed(w)
		}
		return
	}
	if len(parts) == 3 && parts[1] == "refunds" {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		refundID := parts[2]
		h.withAuth(func(w http.ResponseWriter, r *http.Request, p Principal) { h.getRefund(w, r, p, refundID) })(w, r)
		return
	}
	if len(parts) == 4 && parts[1] == "refunds" && (parts[3] == "reconcile" || parts[3] == "recover") {
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		refundID := parts[2]
		h.withAuth(func(w http.ResponseWriter, r *http.Request, p Principal) { h.reconcileRefund(w, r, p, refundID) })(w, r)
		return
	}
	http.NotFound(w, r)
}

func methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
}

func (h *Handler) withAuth(next func(http.ResponseWriter, *http.Request, Principal)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Fields(r.Header.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			writeError(w, http.StatusUnauthorized, "unauthorized", "valid bearer token required")
			return
		}
		principal, ok := h.tokens[parts[1]]
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "invalid bearer token")
			return
		}
		next(w, r, principal)
	}
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) listOrders(w http.ResponseWriter, r *http.Request, principal Principal) {
	rows, err := h.db.QueryContext(r.Context(), `SELECT o.id, o.merchant_id, o.amount_cents, o.currency, o.status, o.provider_channel, COALESCE(f.id, ''), COALESCE(f.status, ''), COALESCE(f.reason, ''), COALESCE(f.updated_at, ''), CASE
		WHEN f.status = 'PROCESSING' THEN 'Refund is processing.'
		WHEN f.status = 'UNKNOWN' THEN 'Channel processing result is pending confirmation. Query the original refund.'
		WHEN f.status = 'SUCCEEDED' THEN 'Refund completed successfully.'
		WHEN f.status = 'FAILED' THEN COALESCE(NULLIF(f.failure_reason, ''), 'Payment channel rejected the refund.')
		ELSE '' END
		FROM orders o LEFT JOIN refunds f ON f.order_id = o.id WHERE o.merchant_id = ? ORDER BY o.id`, principal.MerchantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not load orders")
		return
	}
	defer rows.Close()
	orders := make([]Order, 0)
	for rows.Next() {
		var order Order
		if err := rows.Scan(&order.ID, &order.MerchantID, &order.AmountCents, &order.Currency, &order.Status, &order.ProviderChannel, &order.RefundID, &order.RefundStatus, &order.RefundReason, &order.RefundUpdatedAt, &order.RefundMessage); err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "could not load orders")
			return
		}
		orders = append(orders, order)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not load orders")
		return
	}
	writeJSON(w, http.StatusOK, orders)
}

func (h *Handler) syncPayment(w http.ResponseWriter, r *http.Request, principal Principal, orderID string) {
	if principal.Role != RoleMerchant {
		writeError(w, http.StatusForbidden, "forbidden", "this account is read only")
		return
	}
	order, err := h.loadOrder(r.Context(), orderID, principal.MerchantID)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "order not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not load order")
		return
	}
	if order.ProviderChannel != "mock-unknown" {
		writeError(w, http.StatusConflict, "payment_not_syncable", "payment status cannot be synchronized for this order")
		return
	}
	if order.Status != "PAID" {
		if order.Status != "UNKNOWN" {
			writeError(w, http.StatusConflict, "payment_not_syncable", "payment status cannot be synchronized for this order")
			return
		}
		provider, ok := h.providers[order.ProviderChannel].(PaymentProvider)
		if !ok {
			writeError(w, http.StatusInternalServerError, "provider_unavailable", "payment status provider is unavailable")
			return
		}
		status, err := provider.QueryPayment(r.Context(), order.ID)
		if err != nil {
			writeError(w, http.StatusBadGateway, "provider_unavailable", "could not query payment status")
			return
		}
		if status == "PAID" {
			// The conditional update keeps concurrent synchronization idempotent.
			if _, err := h.db.ExecContext(r.Context(), `UPDATE orders SET status = 'PAID' WHERE id = ? AND merchant_id = ? AND status = 'UNKNOWN' AND provider_channel = 'mock-unknown'`, orderID, principal.MerchantID); err != nil {
				writeError(w, http.StatusInternalServerError, "internal_error", "could not synchronize payment")
				return
			}
		} else if status != "UNKNOWN" {
			writeError(w, http.StatusBadGateway, "provider_unavailable", "payment provider returned an invalid status")
			return
		}
	}
	h.getOrder(w, r, principal, orderID)
}

func (h *Handler) createRefund(w http.ResponseWriter, r *http.Request, principal Principal, orderID string) {
	if principal.Role != RoleMerchant {
		writeError(w, http.StatusForbidden, "forbidden", "this account is read only")
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "request body must be valid JSON")
		return
	}
	headerKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if headerKey == "" || len(headerKey) > 200 {
		writeError(w, http.StatusBadRequest, "invalid_request", "Idempotency-Key header is required and must be at most 200 characters")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_request", "request body must contain one JSON object")
		return
	}
	body.Reason = strings.TrimSpace(body.Reason)
	if body.Reason == "" || len(body.Reason) > 500 {
		writeError(w, http.StatusBadRequest, "invalid_request", "reason is required and must be at most 500 characters")
		return
	}
	if r.Header.Get("X-Demo-Outcome") != "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "refund outcome is fixed by the order channel")
		return
	}

	order, err := h.loadOrder(r.Context(), orderID, principal.MerchantID)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "order not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not load order")
		return
	}
	if existing, err := h.loadByKey(r.Context(), principal.MerchantID, headerKey); err == nil {
		if existing.OrderID != orderID || existing.AmountCents != order.AmountCents || existing.Reason != body.Reason {
			writeError(w, http.StatusConflict, "idempotency_conflict", "idempotency key was already used for a different request")
			return
		}
		writeJSON(w, http.StatusOK, existing)
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not check idempotency key")
		return
	}
	if existing, err := h.loadByOrder(r.Context(), orderID); err == nil {
		writeError(w, http.StatusConflict, "refund_exists", fmt.Sprintf("order already has refund %s", existing.ID))
		return
	} else if !errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not check existing refund")
		return
	}
	if order.Status != "PAID" {
		writeError(w, http.StatusConflict, "order_not_refundable", "only paid orders can be refunded")
		return
	}

	provider := h.providers[order.ProviderChannel]
	if provider == nil {
		writeError(w, http.StatusInternalServerError, "provider_unavailable", "configured payment channel is unavailable")
		return
	}
	refundID, err := randomID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not create refund")
		return
	}
	requestID, err := randomID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not create provider request")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := h.db.BeginTx(r.Context(), nil)
	if err == nil {
		_, err = tx.ExecContext(r.Context(), `INSERT INTO refunds (id, order_id, merchant_id, amount_cents, currency, status, idempotency_key, provider_channel, provider_request_id, reason, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, refundID, order.ID, principal.MerchantID, order.AmountCents, order.Currency, StatusProcessing, headerKey, order.ProviderChannel, requestID, body.Reason, now, now)
		if err == nil {
			err = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
	}
	if err != nil {
		if existing, lookupErr := h.loadByKey(r.Context(), principal.MerchantID, headerKey); lookupErr == nil {
			if existing.OrderID == orderID && existing.AmountCents == order.AmountCents && existing.Reason == body.Reason {
				writeJSON(w, http.StatusOK, existing)
			} else {
				writeError(w, http.StatusConflict, "idempotency_conflict", "idempotency key was already used for a different request")
			}
			return
		}
		if existing, lookupErr := h.loadByOrder(r.Context(), orderID); lookupErr == nil {
			writeError(w, http.StatusConflict, "refund_exists", fmt.Sprintf("order already has refund %s", existing.ID))
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "could not reserve refund")
		return
	}

	result, callErr := provider.SubmitRefund(r.Context(), ProviderRequest{RequestID: requestID, OrderID: order.ID, Amount: order.AmountCents, Currency: order.Currency})
	if callErr != nil {
		result = ProviderResult{Status: StatusUnknown, RequestID: requestID, Reason: "Channel response unavailable; query the original refund."}
	}
	if result.RequestID == "" {
		result.RequestID = requestID
	}
	if !validProviderStatus(result.Status) {
		result.Status = StatusUnknown
		result.Reason = "channel returned an unrecognized status"
	}
	updated, err := h.updateRefund(r.Context(), refundID, result)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "refund was reserved but its result could not be saved")
		return
	}
	writeJSON(w, http.StatusCreated, updated)
}

func (h *Handler) getOrder(w http.ResponseWriter, r *http.Request, principal Principal, orderID string) {
	order, err := h.loadOrder(r.Context(), orderID, principal.MerchantID)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "order not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not load order")
		return
	}
	if refund, err := h.loadByOrder(r.Context(), order.ID); err == nil {
		order.RefundID, order.RefundStatus = refund.ID, refund.Status
		order.RefundReason, order.RefundMessage = refund.Reason, refund.Message
		order.RefundUpdatedAt = refund.UpdatedAt
		order.ProviderRequestID = refund.ProviderRequestID
	} else if !errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not load refund status")
		return
	}
	writeJSON(w, http.StatusOK, order)
}

func (h *Handler) getOrderRefund(w http.ResponseWriter, r *http.Request, principal Principal, orderID string) {
	order, err := h.loadOrder(r.Context(), orderID, principal.MerchantID)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "order not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not load order")
		return
	}
	refund, err := h.loadByOrder(r.Context(), order.ID)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "refund not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not load refund")
		return
	}
	writeJSON(w, http.StatusOK, refund)
}

func (h *Handler) getRefund(w http.ResponseWriter, r *http.Request, principal Principal, refundID string) {
	refund, err := h.loadByID(r.Context(), refundID, principal.MerchantID)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "refund not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not load refund")
		return
	}
	writeJSON(w, http.StatusOK, refund)
}

func (h *Handler) reconcileRefund(w http.ResponseWriter, r *http.Request, principal Principal, refundID string) {
	refund, err := h.loadByID(r.Context(), refundID, principal.MerchantID)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "refund not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not load refund")
		return
	}
	if principal.Role != RoleMerchant {
		writeError(w, http.StatusForbidden, "forbidden", "this account is read only")
		return
	}
	if refund.Status != StatusUnknown && refund.Status != StatusProcessing {
		writeError(w, http.StatusConflict, "not_recoverable", "only unknown or processing refunds can be reconciled")
		return
	}
	provider := h.providers[refund.ProviderChannel]
	if provider == nil {
		writeError(w, http.StatusInternalServerError, "provider_unavailable", "stored payment channel is unavailable")
		return
	}
	result, queryErr := provider.QueryRefund(r.Context(), refund.ProviderRequestID)
	if queryErr != nil {
		result = ProviderResult{Status: StatusUnknown, RequestID: refund.ProviderRequestID, Reason: "Channel result is still unavailable; retry the query."}
	}
	if result.RequestID == "" {
		result.RequestID = refund.ProviderRequestID
	}
	if !validProviderStatus(result.Status) {
		result.Status = StatusUnknown
		result.Reason = "channel returned an unrecognized status"
	}
	updated, err := h.updateRefund(r.Context(), refund.ID, result)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "could not save reconciliation result")
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (h *Handler) loadOrder(ctx context.Context, id, merchantID string) (Order, error) {
	var o Order
	err := h.db.QueryRowContext(ctx, `SELECT id, merchant_id, amount_cents, currency, status, provider_channel FROM orders WHERE id = ? AND merchant_id = ?`, id, merchantID).Scan(&o.ID, &o.MerchantID, &o.AmountCents, &o.Currency, &o.Status, &o.ProviderChannel)
	return o, err
}

const refundColumns = `id, order_id, merchant_id, amount_cents, currency, status, idempotency_key, provider_channel, provider_request_id, reason, failure_reason, created_at, updated_at`

func scanRefund(row interface{ Scan(...interface{}) error }) (Refund, error) {
	var refund Refund
	err := row.Scan(&refund.ID, &refund.OrderID, &refund.MerchantID, &refund.AmountCents, &refund.Currency, &refund.Status, &refund.IdempotencyKey, &refund.ProviderChannel, &refund.ProviderRequestID, &refund.Reason, &refund.FailureReason, &refund.CreatedAt, &refund.UpdatedAt)
	refund.Message = refundMessage(refund)
	return refund, err
}

func refundMessage(refund Refund) string {
	switch refund.Status {
	case StatusProcessing:
		return "Refund is processing."
	case StatusUnknown:
		return "Channel processing result is pending confirmation. Query the original refund."
	case StatusSucceeded:
		return "Refund completed successfully."
	case StatusFailed:
		if refund.FailureReason != "" {
			return refund.FailureReason
		}
		return "Payment channel rejected the refund."
	default:
		return "Refund status is unavailable."
	}
}

func (h *Handler) loadByKey(ctx context.Context, merchantID, key string) (Refund, error) {
	return scanRefund(h.db.QueryRowContext(ctx, `SELECT `+refundColumns+` FROM refunds WHERE merchant_id = ? AND idempotency_key = ?`, merchantID, key))
}

func (h *Handler) loadByOrder(ctx context.Context, orderID string) (Refund, error) {
	return scanRefund(h.db.QueryRowContext(ctx, `SELECT `+refundColumns+` FROM refunds WHERE order_id = ?`, orderID))
}

func (h *Handler) loadByID(ctx context.Context, id, merchantID string) (Refund, error) {
	return scanRefund(h.db.QueryRowContext(ctx, `SELECT `+refundColumns+` FROM refunds WHERE id = ? AND merchant_id = ?`, id, merchantID))
}

func (h *Handler) updateRefund(ctx context.Context, id string, result ProviderResult) (Refund, error) {
	_, err := h.db.ExecContext(ctx, `UPDATE refunds SET
		status = CASE WHEN status IN (?, ?) OR (status = ? AND ? = ?) THEN status ELSE ? END,
		failure_reason = CASE WHEN status IN (?, ?) OR (status = ? AND ? = ?) THEN failure_reason ELSE ? END,
		updated_at = CASE WHEN status IN (?, ?) OR (status = ? AND ? = ?) THEN updated_at ELSE ? END
		WHERE id = ?`, StatusSucceeded, StatusFailed, StatusUnknown, result.Status, StatusProcessing, result.Status,
		StatusSucceeded, StatusFailed, StatusUnknown, result.Status, StatusProcessing, result.Reason,
		StatusSucceeded, StatusFailed, StatusUnknown, result.Status, StatusProcessing, time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return Refund{}, err
	}
	return scanRefund(h.db.QueryRowContext(ctx, `SELECT `+refundColumns+` FROM refunds WHERE id = ?`, id))
}

func randomID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}

func validProviderStatus(status Status) bool {
	return status == StatusProcessing || status == StatusUnknown || status == StatusSucceeded || status == StatusFailed
}

func writeJSON(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"code": code, "message": message})
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "http://localhost:5173")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Idempotency-Key")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
