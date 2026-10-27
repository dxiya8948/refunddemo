package refunds

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
)

type fakeProvider struct {
	mu             sync.Mutex
	refundCalls    int
	queryCalls     int
	lastRequestID  string
	lastQueryID    string
	refundResponse ProviderResult
	queryResponse  ProviderResult
}

func (p *fakeProvider) SubmitRefund(_ context.Context, request ProviderRequest) (ProviderResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.refundCalls++
	p.lastRequestID = request.RequestID
	return p.refundResponse, nil
}

func (p *fakeProvider) QueryRefund(_ context.Context, requestID string) (ProviderResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.queryCalls++
	p.lastQueryID = requestID
	return p.queryResponse, nil
}

func (p *fakeProvider) calls() (int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.refundCalls, p.queryCalls
}

func (p *fakeProvider) requestIDs() (string, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastRequestID, p.lastQueryID
}

func testHandler(t *testing.T, provider Provider) (*sql.DB, http.Handler) {
	t.Helper()
	db, err := OpenDB(filepath.Join(t.TempDir(), "refunds.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := SeedDemoOrders(db); err != nil {
		t.Fatal(err)
	}
	providers := map[string]Provider{"mock-a": provider, "mock-b": provider, "mock-success": provider, "mock-unknown": provider, "mock-fail": provider}
	tokens := map[string]Principal{
		"token-a":          {MerchantID: "merchant-a", Role: RoleMerchant},
		"token-b":          {MerchantID: "merchant-b", Role: RoleMerchant},
		"token-a-readonly": {MerchantID: "merchant-a", Role: RoleReadOnly},
	}
	return db, NewHandler(db, providers, tokens)
}

func request(handler http.Handler, method, path, token string, body interface{}) *httptest.ResponseRecorder {
	var payload bytes.Buffer
	var idempotencyKey string
	switch value := body.(type) {
	case map[string]string:
		idempotencyKey = value["idempotency_key"]
		copy := make(map[string]string, len(value))
		for key, item := range value {
			if key != "idempotency_key" {
				copy[key] = item
			}
		}
		body = copy
	case map[string]interface{}:
		if key, ok := value["idempotency_key"].(string); ok {
			idempotencyKey = key
		}
		copy := make(map[string]interface{}, len(value))
		for key, item := range value {
			if key != "idempotency_key" {
				copy[key] = item
			}
		}
		body = copy
	}
	if body != nil {
		_ = json.NewEncoder(&payload).Encode(body)
	}
	req := httptest.NewRequest(method, path, &payload)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func TestRefundIdempotencyAndPayloadConflict(t *testing.T) {
	provider := &fakeProvider{refundResponse: ProviderResult{Status: StatusSucceeded, RequestID: "provider-1"}}
	db, handler := testHandler(t, provider)
	body := map[string]interface{}{"idempotency_key": "idem-001", "reason": "customer_cancelled"}
	first := request(handler, http.MethodPost, "/api/orders/A-1001/refunds", "token-a", body)
	second := request(handler, http.MethodPost, "/api/orders/A-1001/refunds", "token-a", body)
	if first.Code != http.StatusCreated || second.Code != http.StatusOK {
		t.Fatalf("expected 201 then 200, got %d then %d (%s)", first.Code, second.Code, second.Body.String())
	}
	if _, err := db.Exec(`UPDATE orders SET status = 'REFUNDED' WHERE id = 'A-1001'`); err != nil {
		t.Fatal(err)
	}
	third := request(handler, http.MethodPost, "/api/orders/A-1001/refunds", "token-a", body)
	if third.Code != http.StatusOK {
		t.Fatalf("same-key replay must return the original refund after order status changes, got %d", third.Code)
	}
	refundCalls, _ := provider.calls()
	if refundCalls != 1 {
		t.Fatalf("expected one provider refund call, got %d", refundCalls)
	}
	conflict := request(handler, http.MethodPost, "/api/orders/A-1002/refunds", "token-a", body)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("expected idempotency payload conflict 409, got %d", conflict.Code)
	}
}

func TestDifferentKeysCannotRefundOrderTwice(t *testing.T) {
	provider := &fakeProvider{refundResponse: ProviderResult{Status: StatusSucceeded, RequestID: "provider-1"}}
	_, handler := testHandler(t, provider)
	first := request(handler, http.MethodPost, "/api/orders/A-1001/refunds", "token-a", map[string]string{"idempotency_key": "idem-1", "reason": "customer_cancelled"})
	second := request(handler, http.MethodPost, "/api/orders/A-1001/refunds", "token-a", map[string]string{"idempotency_key": "idem-2", "reason": "customer_cancelled"})
	if first.Code != http.StatusCreated || second.Code != http.StatusConflict {
		t.Fatalf("expected 201 then 409, got %d then %d", first.Code, second.Code)
	}
	refundCalls, _ := provider.calls()
	if refundCalls != 1 {
		t.Fatalf("expected one provider call, got %d", refundCalls)
	}
}

func TestUnpaidOrderCannotBeRefunded(t *testing.T) {
	provider := &fakeProvider{refundResponse: ProviderResult{Status: StatusSucceeded, RequestID: "provider-1"}}
	db, handler := testHandler(t, provider)
	if _, err := db.Exec(`UPDATE orders SET status = 'CANCELLED' WHERE id = 'A-1001'`); err != nil {
		t.Fatal(err)
	}
	response := request(handler, http.MethodPost, "/api/orders/A-1001/refunds", "token-a", map[string]string{"idempotency_key": "unpaid-key", "reason": "customer_cancelled"})
	if response.Code != http.StatusConflict {
		t.Fatalf("expected unpaid order conflict 409, got %d", response.Code)
	}
	refundCalls, _ := provider.calls()
	if refundCalls != 0 {
		t.Fatalf("unpaid order must not reach provider, got %d calls", refundCalls)
	}
}

func TestIdempotencyHeaderIsRequired(t *testing.T) {
	provider := &fakeProvider{refundResponse: ProviderResult{Status: StatusSucceeded, RequestID: "provider-1"}}
	_, handler := testHandler(t, provider)
	requestWithHeader := func(orderID, headerKey string, body interface{}) *httptest.ResponseRecorder {
		var payload bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&payload).Encode(body)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/orders/"+orderID+"/refunds", &payload)
		req.Header.Set("Authorization", "Bearer token-a")
		req.Header.Set("Idempotency-Key", headerKey)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	created := requestWithHeader("A-1003", "header-key", map[string]string{"reason": "duplicate_charge"})
	if created.Code != http.StatusCreated {
		t.Fatalf("expected header-only key to create refund, got %d", created.Code)
	}
	missingKey := request(handler, http.MethodPost, "/api/orders/A-1001/refunds", "token-a", map[string]string{"reason": "customer_cancelled"})
	if missingKey.Code != http.StatusBadRequest {
		t.Fatalf("expected missing Idempotency-Key to return 400, got %d", missingKey.Code)
	}
}

func TestConcurrentDifferentKeysCreateOnlyOneRefund(t *testing.T) {
	provider := &fakeProvider{refundResponse: ProviderResult{Status: StatusSucceeded, RequestID: "provider-1"}}
	_, handler := testHandler(t, provider)
	statuses := make([]int, 2)
	var wg sync.WaitGroup
	for i := range statuses {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			statuses[i] = request(handler, http.MethodPost, "/api/orders/A-1001/refunds", "token-a", map[string]string{"idempotency_key": "race-" + string(rune('1'+i)), "reason": "customer_cancelled"}).Code
		}(i)
	}
	wg.Wait()
	created, conflict := 0, 0
	for _, status := range statuses {
		if status == http.StatusCreated {
			created++
		}
		if status == http.StatusConflict {
			conflict++
		}
	}
	if created != 1 || conflict != 1 {
		t.Fatalf("expected one 201 and one 409, got %v", statuses)
	}
	refundCalls, _ := provider.calls()
	if refundCalls != 1 {
		t.Fatalf("expected one provider call, got %d", refundCalls)
	}
}

func TestUnknownRefundRecoversByPersistedProviderRequest(t *testing.T) {
	provider := &fakeProvider{
		refundResponse: ProviderResult{Status: StatusUnknown, RequestID: "provider-unknown-7"},
		queryResponse:  ProviderResult{Status: StatusSucceeded, RequestID: "provider-unknown-7"},
	}
	_, handler := testHandler(t, provider)
	created := request(handler, http.MethodPost, "/api/orders/A-1002/refunds", "token-a", map[string]string{"idempotency_key": "idem-unknown", "reason": "customer_cancelled"})
	if created.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", created.Code, created.Body.String())
	}
	var result Refund
	if err := json.Unmarshal(created.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusUnknown || result.ProviderRequestID == "" {
		t.Fatalf("unexpected persisted unknown response: %+v", result)
	}
	recovered := request(handler, http.MethodPost, "/api/refunds/"+result.ID+"/reconcile", "token-a", nil)
	if recovered.Code != http.StatusOK {
		t.Fatalf("expected recovery 200, got %d: %s", recovered.Code, recovered.Body.String())
	}
	refundCalls, queryCalls := provider.calls()
	if refundCalls != 1 || queryCalls != 1 {
		t.Fatalf("expected 1 refund and 1 query, got %d and %d", refundCalls, queryCalls)
	}
	submittedID, queriedID := provider.requestIDs()
	if submittedID == "" || queriedID != result.ProviderRequestID || submittedID != result.ProviderRequestID {
		t.Fatalf("reconciliation must query the exact reserved provider request id: submitted=%q queried=%q refund=%q", submittedID, queriedID, result.ProviderRequestID)
	}
}

func TestMerchantIsolationAndReadonlyRole(t *testing.T) {
	provider := &fakeProvider{refundResponse: ProviderResult{Status: StatusSucceeded, RequestID: "provider-1"}}
	_, handler := testHandler(t, provider)
	foreignDetail := request(handler, http.MethodGet, "/api/orders/A-1001", "token-b", nil)
	if foreignDetail.Code != http.StatusNotFound || bytes.Contains(foreignDetail.Body.Bytes(), []byte(`"A-1001"`)) {
		t.Fatalf("merchant B must not see merchant A order detail, got %d %s", foreignDetail.Code, foreignDetail.Body.String())
	}
	foreign := request(handler, http.MethodGet, "/api/orders/A-1001/refunds", "token-b", nil)
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("expected foreign order hidden as 404, got %d", foreign.Code)
	}
	readonly := request(handler, http.MethodPost, "/api/orders/A-1001/refunds", "token-a-readonly", map[string]string{"idempotency_key": "readonly-1", "reason": "customer_cancelled"})
	if readonly.Code != http.StatusForbidden {
		t.Fatalf("expected readonly write 403, got %d", readonly.Code)
	}
	unauthenticated := request(handler, http.MethodGet, "/api/orders", "", nil)
	if unauthenticated.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthenticated 401, got %d", unauthenticated.Code)
	}
	orders := request(handler, http.MethodGet, "/api/orders", "token-a", nil)
	if orders.Code != http.StatusOK || bytes.Contains(orders.Body.Bytes(), []byte(`"B-2001"`)) {
		t.Fatalf("merchant A order list must not expose merchant B orders: %d %s", orders.Code, orders.Body.String())
	}
	foreignWrite := request(handler, http.MethodPost, "/api/orders/B-2001/refunds", "token-a", map[string]string{"idempotency_key": "cross-merchant", "reason": "customer_cancelled"})
	if foreignWrite.Code != http.StatusNotFound {
		t.Fatalf("merchant A cannot refund merchant B order, got %d", foreignWrite.Code)
	}
	bCreated := request(handler, http.MethodPost, "/api/orders/B-2001/refunds", "token-b", map[string]string{"idempotency_key": "b-refund", "reason": "customer_cancelled"})
	if bCreated.Code != http.StatusCreated {
		t.Fatalf("merchant B refund: %d %s", bCreated.Code, bCreated.Body.String())
	}
	var bRefund Refund
	if err := json.Unmarshal(bCreated.Body.Bytes(), &bRefund); err != nil {
		t.Fatal(err)
	}
	for _, methodAndPath := range [][2]string{{http.MethodGet, "/api/refunds/" + bRefund.ID}, {http.MethodPost, "/api/refunds/" + bRefund.ID + "/reconcile"}} {
		response := request(handler, methodAndPath[0], methodAndPath[1], "token-a", nil)
		if response.Code != http.StatusNotFound {
			t.Fatalf("merchant A cannot access merchant B refund through %s %s, got %d", methodAndPath[0], methodAndPath[1], response.Code)
		}
	}
	readOnlyList := request(handler, http.MethodGet, "/api/orders/A-1001", "token-a-readonly", nil)
	if readOnlyList.Code != http.StatusOK {
		t.Fatalf("read-only account must be able to view own merchant order, got %d", readOnlyList.Code)
	}
}

func TestUnknownRefundCanReconcileAfterDatabaseReopen(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "restart.db")
	db, err := OpenDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := SeedDemoOrders(db); err != nil {
		t.Fatal(err)
	}
	provider := NewDemoProvider(db, "mock-unknown")
	tokens := map[string]Principal{"token-a": {MerchantID: "merchant-a", Role: RoleMerchant}}
	firstHandler := NewHandler(db, map[string]Provider{"mock-unknown": provider}, tokens)
	created := request(firstHandler, http.MethodPost, "/api/orders/A-1002/refunds", "token-a", map[string]string{"idempotency_key": "restart-key", "reason": "customer_cancelled"})
	if created.Code != http.StatusCreated {
		t.Fatalf("expected create 201, got %d: %s", created.Code, created.Body.String())
	}
	var refund Refund
	if err := json.Unmarshal(created.Body.Bytes(), &refund); err != nil {
		t.Fatal(err)
	}
	if refund.Status != StatusUnknown {
		t.Fatalf("expected UNKNOWN before restart, got %s", refund.Status)
	}
	var accepted, submitCalls int
	if err := db.QueryRow(`SELECT COUNT(*), COALESCE(MAX(submit_calls), 0) FROM provider_records WHERE provider_channel = 'mock-unknown' AND provider_request_id = ?`, refund.ProviderRequestID).Scan(&accepted, &submitCalls); err != nil {
		t.Fatal(err)
	}
	if accepted != 1 || submitCalls != 1 {
		t.Fatalf("unknown response must still have one durable acceptance and one submit, accepted=%d calls=%d", accepted, submitCalls)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenDB(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	newProcessProvider := NewDemoProvider(reopened, "mock-unknown")
	secondHandler := NewHandler(reopened, map[string]Provider{"mock-unknown": newProcessProvider}, tokens)
	recovered := request(secondHandler, http.MethodPost, "/api/refunds/"+refund.ID+"/reconcile", "token-a", nil)
	if recovered.Code != http.StatusOK {
		t.Fatalf("expected recovery 200 after database reopen, got %d: %s", recovered.Code, recovered.Body.String())
	}
	if err := json.Unmarshal(recovered.Body.Bytes(), &refund); err != nil {
		t.Fatal(err)
	}
	if refund.Status != StatusSucceeded || refund.ProviderRequestID == "" {
		t.Fatalf("expected reconciled success with persisted request id, got %+v", refund)
	}
	if err := reopened.QueryRow(`SELECT submit_calls FROM provider_records WHERE provider_channel = 'mock-unknown' AND provider_request_id = ?`, refund.ProviderRequestID).Scan(&submitCalls); err != nil || submitCalls != 1 {
		t.Fatalf("reconciliation must query, not resubmit; submit calls=%d err=%v", submitCalls, err)
	}
}

func TestExplicitProviderFailureAndUnpaidOrderDoNotCreateAcceptance(t *testing.T) {
	db, err := OpenDB(filepath.Join(t.TempDir(), "provider-failure.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := SeedDemoOrders(db); err != nil {
		t.Fatal(err)
	}
	provider := NewDemoProvider(db, "mock-fail")
	tokens := map[string]Principal{"token-a": {MerchantID: "merchant-a", Role: RoleMerchant}}
	handler := NewHandler(db, map[string]Provider{"mock-fail": provider, "mock-success": NewDemoProvider(db, "mock-success")}, tokens)
	failed := request(handler, http.MethodPost, "/api/orders/A-1003/refunds", "token-a", map[string]string{"idempotency_key": "declined", "reason": "duplicate_charge"})
	if failed.Code != http.StatusCreated || !bytes.Contains(failed.Body.Bytes(), []byte(`"status":"FAILED"`)) || !bytes.Contains(failed.Body.Bytes(), []byte(`"reason":"duplicate_charge"`)) {
		t.Fatalf("explicit channel rejection should remain a readable refund record, got %d: %s", failed.Code, failed.Body.String())
	}
	unpaid := request(handler, http.MethodPost, "/api/orders/A-1004/refunds", "token-a", map[string]string{"idempotency_key": "unpaid", "reason": "customer_cancelled"})
	if unpaid.Code != http.StatusConflict {
		t.Fatalf("unpaid order must be rejected, got %d: %s", unpaid.Code, unpaid.Body.String())
	}
	var failedRecords, unpaidRecords int
	if err := db.QueryRow(`SELECT COUNT(*) FROM provider_records WHERE provider_channel = 'mock-fail' AND order_id = 'A-1003'`).Scan(&failedRecords); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM provider_records WHERE order_id = 'A-1004'`).Scan(&unpaidRecords); err != nil {
		t.Fatal(err)
	}
	if failedRecords != 1 || unpaidRecords != 0 {
		t.Fatalf("channel ledger mismatch: failed=%d unpaid=%d", failedRecords, unpaidRecords)
	}
	orders := request(handler, http.MethodGet, "/api/orders", "token-a", nil)
	if !bytes.Contains(orders.Body.Bytes(), []byte(`"refund_message":"Payment channel rejected the refund.`)) {
		t.Fatalf("order list should carry a readable channel rejection message: %s", orders.Body.String())
	}
}

func TestDemoProviderSubmissionIsDurablyIdempotent(t *testing.T) {
	db, err := OpenDB(filepath.Join(t.TempDir(), "provider-idempotency.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	provider := NewDemoProvider(db, "mock-unknown")
	request := ProviderRequest{RequestID: "stable-provider-id", OrderID: "A-1002", Amount: 8800}
	first, firstErr := provider.SubmitRefund(context.Background(), request)
	if firstErr == nil || first.Status != StatusUnknown {
		t.Fatalf("first unknown-mode submit must persist then time out, result=%+v err=%v", first, firstErr)
	}
	second, secondErr := provider.SubmitRefund(context.Background(), request)
	if secondErr != nil || second.Status != StatusSucceeded {
		t.Fatalf("same request and parameters must return the saved acceptance, result=%+v err=%v", second, secondErr)
	}
	request.Amount++
	if _, err := provider.SubmitRefund(context.Background(), request); err == nil {
		t.Fatal("same provider request id with different parameters must be rejected")
	}
	var accepted, calls int
	if err := db.QueryRow(`SELECT COUNT(*), COALESCE(MAX(submit_calls), 0) FROM provider_records WHERE provider_channel = 'mock-unknown'`).Scan(&accepted, &calls); err != nil {
		t.Fatal(err)
	}
	if accepted != 1 || calls != 2 {
		t.Fatalf("want one accepted record and two submit attempts, accepted=%d calls=%d", accepted, calls)
	}
}

func TestSuccessfulRefundMatchesBusinessAndProviderLedgers(t *testing.T) {
	db, err := OpenDB(filepath.Join(t.TempDir(), "provider-success.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := SeedDemoOrders(db); err != nil {
		t.Fatal(err)
	}
	tokens := map[string]Principal{"token-a": {MerchantID: "merchant-a", Role: RoleMerchant}}
	provider := NewDemoProvider(db, "mock-success")
	handler := NewHandler(db, map[string]Provider{"mock-success": provider}, tokens)
	payload := map[string]string{"idempotency_key": "normal-refund", "reason": "customer_cancelled"}
	created := request(handler, http.MethodPost, "/api/orders/A-1001/refunds", "token-a", payload)
	if created.Code != http.StatusCreated {
		t.Fatalf("normal refund should be created, got %d: %s", created.Code, created.Body.String())
	}
	var refund Refund
	if err := json.Unmarshal(created.Body.Bytes(), &refund); err != nil {
		t.Fatal(err)
	}
	if refund.Status != StatusSucceeded || refund.AmountCents != 19900 {
		t.Fatalf("normal refund should return the full successful amount, got %+v", refund)
	}
	var orderID, channelStatus, channelRequestID string
	var businessAmount, channelAmount int64
	if err := db.QueryRow(`SELECT order_id, amount_cents, status, provider_request_id FROM refunds WHERE id = ?`, refund.ID).Scan(&orderID, &businessAmount, &channelStatus, &channelRequestID); err != nil {
		t.Fatal(err)
	}
	if orderID != "A-1001" || businessAmount != 19900 || channelStatus != string(StatusSucceeded) || channelRequestID != refund.ProviderRequestID {
		t.Fatalf("business refund record mismatch: order=%s amount=%d status=%s provider_request=%s", orderID, businessAmount, channelStatus, channelRequestID)
	}
	if err := db.QueryRow(`SELECT amount_cents, status FROM provider_records WHERE provider_channel = 'mock-success' AND provider_request_id = ?`, refund.ProviderRequestID).Scan(&channelAmount, &channelStatus); err != nil {
		t.Fatal(err)
	}
	if channelAmount != businessAmount || channelStatus != string(StatusSucceeded) {
		t.Fatalf("channel acceptance does not match business refund: amount=%d status=%s", channelAmount, channelStatus)
	}
	replay := request(handler, http.MethodPost, "/api/orders/A-1001/refunds", "token-a", payload)
	if replay.Code != http.StatusOK {
		t.Fatalf("same request should replay saved success, got %d", replay.Code)
	}
	var records, calls int
	if err := db.QueryRow(`SELECT COUNT(*), MAX(submit_calls) FROM provider_records WHERE provider_channel = 'mock-success'`).Scan(&records, &calls); err != nil {
		t.Fatal(err)
	}
	if records != 1 || calls != 1 {
		t.Fatalf("idempotent replay should produce one acceptance and one submit, records=%d calls=%d", records, calls)
	}
}

func TestRefundRequestCannotOverrideSeededChannelOutcome(t *testing.T) {
	db, _ := testHandler(t, &fakeProvider{})
	handler := NewHandler(db, map[string]Provider{"mock-success": NewDemoProvider(db, "mock-success")}, map[string]Principal{"token-a": {MerchantID: "merchant-a", Role: RoleMerchant}})
	for _, outcome := range []string{"success", "insufficient_funds", "response_lost"} {
		req := httptest.NewRequest(http.MethodPost, "/api/orders/A-1001/refunds", bytes.NewBufferString(`{"reason":"customer_cancelled"}`))
		req.Header.Set("Authorization", "Bearer token-a")
		req.Header.Set("Idempotency-Key", "invalid-"+outcome)
		req.Header.Set("X-Demo-Outcome", outcome)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("outcome %q should be rejected, got %d: %s", outcome, response.Code, response.Body.String())
		}
	}
	var records int
	if err := db.QueryRow(`SELECT COUNT(*) FROM provider_records`).Scan(&records); err != nil || records != 0 {
		t.Fatalf("override attempts must not reach channel, records=%d err=%v", records, err)
	}
}

func TestIdempotencyKeyScopeIncludesMerchant(t *testing.T) {
	provider := &fakeProvider{refundResponse: ProviderResult{Status: StatusSucceeded, RequestID: "provider-scoped"}}
	_, handler := testHandler(t, provider)
	a := request(handler, http.MethodPost, "/api/orders/A-1001/refunds", "token-a", map[string]string{"idempotency_key": "shared-key", "reason": "customer_cancelled"})
	b := request(handler, http.MethodPost, "/api/orders/B-2001/refunds", "token-b", map[string]string{"idempotency_key": "shared-key", "reason": "customer_cancelled"})
	if a.Code != http.StatusCreated || b.Code != http.StatusCreated {
		t.Fatalf("same idempotency key is independent per merchant, got %d then %d", a.Code, b.Code)
	}
}

func TestLegacyRefundSchemaMigratesReasonAndScopedKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE orders (id TEXT PRIMARY KEY, merchant_id TEXT NOT NULL, amount_cents INTEGER NOT NULL, currency TEXT NOT NULL, status TEXT NOT NULL, provider_channel TEXT NOT NULL);
CREATE TABLE refunds (id TEXT PRIMARY KEY, order_id TEXT NOT NULL UNIQUE REFERENCES orders(id), merchant_id TEXT NOT NULL, amount_cents INTEGER NOT NULL, currency TEXT NOT NULL, status TEXT NOT NULL, idempotency_key TEXT NOT NULL UNIQUE, provider_channel TEXT NOT NULL, provider_request_id TEXT NOT NULL, failure_reason TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
INSERT INTO orders VALUES ('legacy-order', 'merchant-a', 100, 'USD', 'PAID', 'mock-success');
INSERT INTO refunds VALUES ('legacy-refund', 'legacy-order', 'merchant-a', 100, 'USD', 'UNKNOWN', 'legacy-key', 'mock-success', 'legacy-provider', '', '2026-10-27T00:00:00Z', '2026-10-27T00:00:00Z');`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	var reason string
	if err := upgraded.QueryRow(`SELECT reason FROM refunds WHERE id = 'legacy-refund'`).Scan(&reason); err != nil || reason != "" {
		t.Fatalf("migration must preserve legacy refund and add empty reason, reason=%q err=%v", reason, err)
	}
	if _, err := upgraded.Exec(`INSERT INTO orders VALUES ('legacy-order-b', 'merchant-b', 100, 'USD', 'PAID', 'mock-success')`); err != nil {
		t.Fatal(err)
	}
	if _, err := upgraded.Exec(`INSERT INTO refunds (id, order_id, merchant_id, amount_cents, currency, status, idempotency_key, provider_channel, provider_request_id, reason, created_at, updated_at) VALUES ('legacy-refund-b', 'legacy-order-b', 'merchant-b', 100, 'USD', 'PROCESSING', 'legacy-key', 'mock-success', 'legacy-provider-b', 'reason', '2026-10-27T00:00:00Z', '2026-10-27T00:00:00Z')`); err != nil {
		t.Fatalf("same key across merchants should be allowed after migration: %v", err)
	}
}

func TestTerminalRefundStatusCannotRegress(t *testing.T) {
	db, _ := testHandler(t, &fakeProvider{})
	h := &Handler{db: db}
	if _, err := db.Exec(`INSERT INTO orders (id, merchant_id, amount_cents, currency, status, provider_channel) VALUES ('terminal-order', 'merchant-a', 100, 'USD', 'PAID', 'mock-a')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO refunds (id, order_id, merchant_id, amount_cents, currency, status, idempotency_key, provider_channel, provider_request_id, created_at, updated_at) VALUES ('terminal-refund', 'terminal-order', 'merchant-a', 100, 'USD', 'SUCCEEDED', 'terminal-key', 'mock-a', 'provider-terminal', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	refund, err := h.updateRefund(context.Background(), "terminal-refund", ProviderResult{Status: StatusUnknown, RequestID: "provider-terminal", Reason: "late timeout"})
	if err != nil {
		t.Fatal(err)
	}
	if refund.Status != StatusSucceeded {
		t.Fatalf("terminal status must remain SUCCEEDED, got %s", refund.Status)
	}
}

func TestContractSeedDataIsRepeatableAndPreservesRefunds(t *testing.T) {
	db, _ := testHandler(t, &fakeProvider{})
	want := map[string]struct {
		merchant string
		amount   int64
		status   string
	}{
		"A-1001": {"merchant-a", 19900, "PAID"},
		"A-1002": {"merchant-a", 8800, "PAID"},
		"A-1003": {"merchant-a", 5600, "PAID"},
		"A-1004": {"merchant-a", 12900, "UNPAID"},
		"B-2001": {"merchant-b", 25600, "PAID"},
	}
	for id, expected := range want {
		var merchant, status, currency string
		var amount int64
		if err := db.QueryRow(`SELECT merchant_id, amount_cents, currency, status FROM orders WHERE id = ?`, id).Scan(&merchant, &amount, &currency, &status); err != nil {
			t.Fatalf("missing contract seed order %s: %v", id, err)
		}
		if merchant != expected.merchant || amount != expected.amount || currency != "CNY" || status != expected.status {
			t.Errorf("order %s = %s/%d/%s/%s, want %s/%d/CNY/%s", id, merchant, amount, currency, status, expected.merchant, expected.amount, expected.status)
		}
	}
	if _, err := db.Exec(`INSERT INTO refunds (id, order_id, merchant_id, amount_cents, currency, status, idempotency_key, provider_channel, provider_request_id, created_at, updated_at) VALUES ('seed-keep', 'A-1001', 'merchant-a', 19900, 'CNY', 'UNKNOWN', 'seed-keep-key', 'mock-success', 'seed-provider', '2026-10-27T00:00:00Z', '2026-10-27T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE orders SET currency = 'USD' WHERE id = 'A-1001'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE refunds SET currency = 'USD' WHERE id = 'seed-keep'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO orders (id, merchant_id, amount_cents, currency, status, provider_channel) VALUES ('custom-order', 'merchant-a', 100, 'USD', 'PAID', 'mock-success')`); err != nil {
		t.Fatal(err)
	}
	if err := SeedDemoOrders(db); err != nil {
		t.Fatal(err)
	}
	var orderCurrency, refundCurrency, customCurrency string
	if err := db.QueryRow(`SELECT currency FROM orders WHERE id = 'A-1001'`).Scan(&orderCurrency); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT currency FROM refunds WHERE id = 'seed-keep'`).Scan(&refundCurrency); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT currency FROM orders WHERE id = 'custom-order'`).Scan(&customCurrency); err != nil {
		t.Fatal(err)
	}
	if orderCurrency != "CNY" || refundCurrency != "CNY" || customCurrency != "USD" {
		t.Fatalf("seed currency migration must preserve refund identity and unrelated data: order=%q refund=%q custom=%q", orderCurrency, refundCurrency, customCurrency)
	}
}

func TestRefundReasonIsRequiredAndPartOfIdempotencyPayload(t *testing.T) {
	provider := &fakeProvider{refundResponse: ProviderResult{Status: StatusSucceeded, RequestID: "provider-reason"}}
	_, handler := testHandler(t, provider)
	post := func(key, orderID, reason string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/orders/"+orderID+"/refunds", bytes.NewBufferString(`{"reason":"`+reason+`"}`))
		req.Header.Set("Authorization", "Bearer token-a")
		req.Header.Set("Idempotency-Key", key)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		return res
	}
	missing := request(handler, http.MethodPost, "/api/orders/A-1001/refunds", "token-a", map[string]string{})
	if missing.Code != http.StatusBadRequest && missing.Code != http.StatusUnprocessableEntity {
		t.Fatalf("missing reason must be rejected, got %d: %s", missing.Code, missing.Body.String())
	}
	modeOverride := request(handler, http.MethodPost, "/api/orders/A-1001/refunds", "token-a", map[string]interface{}{"idempotency_key": "override-key", "reason": "customer_cancelled", "success": true})
	if modeOverride.Code != http.StatusBadRequest {
		t.Fatalf("request body must not override seeded provider mode, got %d", modeOverride.Code)
	}
	first := post("reason-key", "A-1001", "customer_cancelled")
	if first.Code != http.StatusCreated {
		t.Fatalf("create with reason: got %d: %s", first.Code, first.Body.String())
	}
	var refund Refund
	if err := json.Unmarshal(first.Body.Bytes(), &refund); err != nil {
		t.Fatal(err)
	}
	if refund.Reason != "customer_cancelled" {
		t.Fatalf("reason not returned from persisted refund: %+v", refund)
	}
	replay := post("reason-key", "A-1001", "customer_cancelled")
	if replay.Code != http.StatusOK {
		t.Fatalf("same key and payload must replay, got %d", replay.Code)
	}
	conflict := post("reason-key", "A-1001", "duplicate_charge")
	if conflict.Code != http.StatusConflict {
		t.Fatalf("same key with different reason must conflict, got %d", conflict.Code)
	}
	refundCalls, _ := provider.calls()
	if refundCalls != 1 {
		t.Fatalf("replays/conflicts must not resubmit to channel, calls=%d", refundCalls)
	}
}

func TestOrderDetailAndReconcileContract(t *testing.T) {
	provider := &fakeProvider{refundResponse: ProviderResult{Status: StatusUnknown, RequestID: "provider-detail"}, queryResponse: ProviderResult{Status: StatusSucceeded, RequestID: "provider-detail"}}
	_, handler := testHandler(t, provider)
	order := request(handler, http.MethodGet, "/api/orders/A-1001", "token-a", nil)
	if order.Code != http.StatusOK || !bytes.Contains(order.Body.Bytes(), []byte(`"amount_cents":19900`)) {
		t.Fatalf("order detail must return scoped order and amount, got %d: %s", order.Code, order.Body.String())
	}
	if bytes.Contains(order.Body.Bytes(), []byte(`"provider_request_id"`)) {
		t.Fatalf("order without refund must not expose a channel request id: %s", order.Body.String())
	}
	foreign := request(handler, http.MethodGet, "/api/orders/B-2001", "token-a", nil)
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("foreign order detail should be hidden, got %d", foreign.Code)
	}
	created := request(handler, http.MethodPost, "/api/orders/A-1001/refunds", "token-a", map[string]string{"idempotency_key": "detail-key", "reason": "customer_cancelled"})
	if created.Code != http.StatusCreated {
		t.Fatalf("create unknown refund: %d %s", created.Code, created.Body.String())
	}
	var refund Refund
	if err := json.Unmarshal(created.Body.Bytes(), &refund); err != nil {
		t.Fatal(err)
	}
	detail := request(handler, http.MethodGet, "/api/orders/A-1001", "token-a", nil)
	if detail.Code != http.StatusOK {
		t.Fatalf("order detail after refund: %d %s", detail.Code, detail.Body.String())
	}
	var detailOrder Order
	if err := json.Unmarshal(detail.Body.Bytes(), &detailOrder); err != nil {
		t.Fatal(err)
	}
	if detailOrder.ProviderRequestID == "" || detailOrder.ProviderRequestID != refund.ProviderRequestID {
		t.Fatalf("order detail must expose the persisted channel request id: detail=%q refund=%q", detailOrder.ProviderRequestID, refund.ProviderRequestID)
	}
	foreignRefundDetail := request(handler, http.MethodGet, "/api/orders/A-1001", "token-b", nil)
	if foreignRefundDetail.Code != http.StatusNotFound {
		t.Fatalf("merchant B must not see merchant A refund detail, got %d", foreignRefundDetail.Code)
	}
	readonly := request(handler, http.MethodPost, "/api/refunds/"+refund.ID+"/reconcile", "token-a-readonly", nil)
	if readonly.Code != http.StatusForbidden {
		t.Fatalf("read-only reconciliation must return 403, got %d", readonly.Code)
	}
	reconciled := request(handler, http.MethodPost, "/api/refunds/"+refund.ID+"/reconcile", "token-a", nil)
	if reconciled.Code != http.StatusOK || !bytes.Contains(reconciled.Body.Bytes(), []byte(`"status":"SUCCEEDED"`)) {
		t.Fatalf("reconcile should return persisted success, got %d: %s", reconciled.Code, reconciled.Body.String())
	}
}

func TestOrdersExposePersistedRefundUpdateTime(t *testing.T) {
	provider := &fakeProvider{refundResponse: ProviderResult{Status: StatusUnknown, RequestID: "provider-time"}}
	_, handler := testHandler(t, provider)
	created := request(handler, http.MethodPost, "/api/orders/A-1002/refunds", "token-a", map[string]string{"idempotency_key": "time-key", "reason": "customer_cancelled"})
	if created.Code != http.StatusCreated {
		t.Fatalf("create refund: %d %s", created.Code, created.Body.String())
	}
	var refund Refund
	if err := json.Unmarshal(created.Body.Bytes(), &refund); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/orders", "/api/orders/A-1002"} {
		response := request(handler, http.MethodGet, path, "token-a", nil)
		if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"refund_updated_at":"`+refund.UpdatedAt+`"`)) {
			t.Fatalf("%s must expose the persisted refund time, got %d: %s", path, response.Code, response.Body.String())
		}
	}
}

func TestTenConcurrentRefundRequestsHaveNoServerErrors(t *testing.T) {
	db, err := OpenDB(filepath.Join(t.TempDir(), "concurrent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := SeedDemoOrders(db); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(db, map[string]Provider{"mock-success": NewDemoProvider(db, "mock-success")}, map[string]Principal{"token-a": {MerchantID: "merchant-a", Role: RoleMerchant}})
	statuses := make([]int, 10)
	var wg sync.WaitGroup
	for i := range statuses {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			statuses[i] = request(handler, http.MethodPost, "/api/orders/A-1001/refunds", "token-a", map[string]string{"idempotency_key": fmt.Sprintf("race-%d", i), "reason": "customer_cancelled"}).Code
		}(i)
	}
	wg.Wait()
	for _, status := range statuses {
		if status >= http.StatusInternalServerError || (status != http.StatusCreated && status != http.StatusConflict) {
			t.Fatalf("unexpected concurrent response codes: %v", statuses)
		}
	}
	createdCount := 0
	for _, status := range statuses {
		if status == http.StatusCreated {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("exactly one request should create the refund, got %v", statuses)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM refunds WHERE order_id = 'A-1001'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("want exactly one refund, count=%d err=%v", count, err)
	}
	var accepted, submitCalls int
	if err := db.QueryRow(`SELECT COUNT(*), COALESCE(MAX(submit_calls), 0) FROM provider_records WHERE order_id = 'A-1001'`).Scan(&accepted, &submitCalls); err != nil || accepted != 1 || submitCalls != 1 {
		t.Fatalf("want one durable channel acceptance and one submit, accepted=%d calls=%d err=%v", accepted, submitCalls, err)
	}
	var requestID, channelStatus string
	var amount int64
	if err := db.QueryRow(`SELECT provider_request_id, status, amount_cents FROM provider_records WHERE order_id = 'A-1001'`).Scan(&requestID, &channelStatus, &amount); err != nil || requestID == "" || channelStatus != string(StatusSucceeded) || amount != 19900 {
		t.Fatalf("durable channel outcome mismatch: request=%q status=%q amount=%d err=%v", requestID, channelStatus, amount, err)
	}
}
