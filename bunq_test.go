package bunq

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestUnmarshalID(t *testing.T) {
	body := `{"Response":[{"Id":{"id":42}}]}`
	id, err := unmarshalID([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != 42 {
		t.Errorf("expected 42, got %d", id)
	}
}

func TestUnmarshalUUID(t *testing.T) {
	body := `{"Response":[{"Uuid":{"uuid":"abc-123"}}]}`
	uuid, err := unmarshalUUID([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if uuid != "abc-123" {
		t.Errorf("expected abc-123, got %s", uuid)
	}
}

func TestUnmarshalObject(t *testing.T) {
	body := `{"Response":[{"Payment":{"id":1,"description":"test","amount":{"value":"10.00","currency":"EUR"}}}]}`
	payment, err := unmarshalObject[Payment]([]byte(body), "Payment")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if payment.ID != 1 {
		t.Errorf("expected ID 1, got %d", payment.ID)
	}
	if payment.Description != "test" {
		t.Errorf("expected description test, got %s", payment.Description)
	}
	if payment.Amount == nil || payment.Amount.Value != "10.00" {
		t.Errorf("expected amount 10.00, got %v", payment.Amount)
	}
}

func TestUnmarshalList(t *testing.T) {
	body := `{"Response":[{"Payment":{"id":1}},{"Payment":{"id":2}}],"Pagination":{"older_url":"/v1/user/1/monetary-account/2/payment?older_id=100&count=10","newer_url":"/v1/user/1/monetary-account/2/payment?newer_id=3&count=10"}}`
	resp, err := unmarshalList[Payment]([]byte(body), "Payment")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(resp.Items))
	}
	if resp.Items[0].ID != 1 {
		t.Errorf("expected ID 1, got %d", resp.Items[0].ID)
	}
	if resp.Items[1].ID != 2 {
		t.Errorf("expected ID 2, got %d", resp.Items[1].ID)
	}
	if resp.Pagination == nil {
		t.Fatal("expected pagination")
	}
	olderID, ok := resp.Pagination.olderID()
	if !ok {
		t.Fatal("expected olderID to be present")
	}
	if olderID != 100 {
		t.Errorf("expected olderID 100, got %d", olderID)
	}
	newerID, ok := resp.Pagination.newerID()
	if !ok {
		t.Fatal("expected newerID to be present")
	}
	if newerID != 3 {
		t.Errorf("expected newerID 3, got %d", newerID)
	}
}

func TestPaginationNilAndEmpty(t *testing.T) {
	// No pagination in response
	body := `{"Response":[{"Payment":{"id":1}}]}`
	resp, err := unmarshalList[Payment]([]byte(body), "Payment")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, ok := resp.Pagination.olderID()
	if ok {
		t.Error("expected no olderID for nil pagination")
	}

	// Empty pagination (last page)
	body = `{"Response":[{"Payment":{"id":1}}],"Pagination":{}}`
	resp, err = unmarshalList[Payment]([]byte(body), "Payment")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, ok = resp.Pagination.olderID()
	if ok {
		t.Error("expected no olderID for empty pagination")
	}
}

func TestNewAPIError(t *testing.T) {
	body := `{"Error":[{"error_description":"bad request"},{"error_description":"invalid field"}]}`
	err := newAPIError(400, "resp-123", []byte(body))

	var badReq *BadRequestError
	if !isErr(err, &badReq) {
		t.Fatalf("expected BadRequestError, got %T", err)
	}
	if badReq.StatusCode != 400 {
		t.Errorf("expected 400, got %d", badReq.StatusCode)
	}
	if len(badReq.Messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(badReq.Messages))
	}
}

func isErr[T any](err error, target *T) bool {
	// Simple type assertion helper
	switch e := err.(type) {
	case T:
		*target = e
		return true
	default:
		return false
	}
}

func TestListOptions(t *testing.T) {
	opts := &ListOptions{Count: 10, OlderID: 5}
	params := opts.toParams()
	if params["count"] != "10" {
		t.Errorf("expected count=10, got %s", params["count"])
	}
	if params["older_id"] != "5" {
		t.Errorf("expected older_id=5, got %s", params["older_id"])
	}
	if _, ok := params["newer_id"]; ok {
		t.Error("expected no newer_id")
	}
}

func TestFlexFloat64_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    float64
		wantErr bool
	}{
		{"string value", `{"v":"75.50"}`, 75.50, false},
		{"number value", `{"v":75.50}`, 75.50, false},
		{"zero string", `{"v":"0"}`, 0, false},
		{"zero number", `{"v":0}`, 0, false},
		{"integer string", `{"v":"100"}`, 100, false},
		{"negative string", `{"v":"-3.14"}`, -3.14, false},
		{"empty string", `{"v":""}`, 0, true},
		{"null value", `{"v":null}`, 0, false},
		{"absent field", `{}`, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var s struct {
				V FlexFloat64 `json:"v"`
			}
			err := json.Unmarshal([]byte(tt.input), &s)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if float64(s.V) != tt.want {
				t.Errorf("got %v, want %v", s.V, tt.want)
			}
		})
	}
}

func TestFlexFloat64_MarshalJSON(t *testing.T) {
	s := struct {
		V FlexFloat64 `json:"v"`
	}{V: 42.5}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(b) != `{"v":42.5}` {
		t.Errorf("got %s, want %s", b, `{"v":42.5}`)
	}
}

func TestUnmarshalList_SavingsGoalProgress(t *testing.T) {
	// Reproduces the real bug: API returns savings_goal_progress as a string
	body := `{"Response":[{"MonetaryAccountSavings":{"id":123,"savings_goal_progress":"75.50"}}],"Pagination":{}}`
	resp, err := unmarshalList[MonetaryAccountSavings]([]byte(body), "MonetaryAccountSavings")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(resp.Items))
	}
	if float64(resp.Items[0].SavingsGoalProgress) != 75.50 {
		t.Errorf("expected 75.50, got %v", resp.Items[0].SavingsGoalProgress)
	}
}

func TestAmountMarshal(t *testing.T) {
	a := Amount{Value: "10.00", Currency: "EUR"}
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	expected := `{"value":"10.00","currency":"EUR"}`
	if string(b) != expected {
		t.Errorf("expected %s, got %s", expected, string(b))
	}
}

func TestNewAmount(t *testing.T) {
	a := NewAmount(10.50, "EUR")
	if a.Value != "10.50" {
		t.Errorf("expected 10.50, got %s", a.Value)
	}
	if a.Currency != "EUR" {
		t.Errorf("expected EUR, got %s", a.Currency)
	}

	// Integer amount should not have unnecessary decimals
	a = NewAmount(100, "USD")
	if a.Value != "100.00" {
		t.Errorf("expected 100.00, got %s", a.Value)
	}

	// Tiny fraction
	a = NewAmount(0.01, "EUR")
	if a.Value != "0.01" {
		t.Errorf("expected 0.01, got %s", a.Value)
	}
}

func TestAmountFloat64(t *testing.T) {
	a := NewAmount(42.99, "EUR")
	if a.Float64() != 42.99 {
		t.Errorf("expected 42.99, got %f", a.Float64())
	}

	// From API response (string value)
	a = &Amount{Value: "123.45", Currency: "EUR"}
	if a.Float64() != 123.45 {
		t.Errorf("expected 123.45, got %f", a.Float64())
	}

	// Invalid value returns 0
	a = &Amount{Value: "not-a-number", Currency: "EUR"}
	if a.Float64() != 0 {
		t.Errorf("expected 0, got %f", a.Float64())
	}
}

func TestSecuritySignVerify(t *testing.T) {
	key, err := generateRSAKeyPair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}

	body := []byte(`{"test":"data"}`)

	sig, err := signRequest(key, body)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	if err := verifyResponse(&key.PublicKey, body, sig); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestPublicKeyPEM(t *testing.T) {
	key, err := generateRSAKeyPair()
	if err != nil {
		t.Fatalf("keygen: %v", err)
	}

	pem := publicKeyToPEM(&key.PublicKey)
	if pem == "" {
		t.Fatal("expected non-empty PEM")
	}

	pub, err := parsePublicKeyPEM(pem)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	if !key.PublicKey.Equal(pub) {
		t.Error("parsed key doesn't match original")
	}
}

func TestRetryOn429(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n <= 2 {
			// Return Retry-After: 0 to avoid slow tests
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprintf(w, `{"Error":[{"error_description":"Too many requests"}]}`)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"Response":[{"Id":{"id":42}}]}`)
	}))
	defer srv.Close()

	c := &Client{
		httpClient: srv.Client(),
		baseURL:    srv.URL,
	}

	body, _, err := c.request(context.Background(), http.MethodGet, "test", nil, false)
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}

	id, err := unmarshalID(body)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if id != 42 {
		t.Errorf("expected 42, got %d", id)
	}
	if n := calls.Load(); n != 3 {
		t.Errorf("expected 3 calls, got %d", n)
	}
}

func TestRetryOn429_ExhaustsRetries(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
		fmt.Fprintf(w, `{"Error":[{"error_description":"Too many requests"}]}`)
	}))
	defer srv.Close()

	c := &Client{
		httpClient: srv.Client(),
		baseURL:    srv.URL,
	}

	_, _, err := c.request(context.Background(), http.MethodGet, "test", nil, false)
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	var tooMany *TooManyRequestsError
	if !isErr(err, &tooMany) {
		t.Fatalf("expected TooManyRequestsError, got %T: %v", err, err)
	}
	if n := calls.Load(); n != 6 {
		t.Errorf("expected 6 calls (1 + 5 retries), got %d", n)
	}
}

func TestRetryOn429_ExponentialBackoff(t *testing.T) {
	var timestamps []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		timestamps = append(timestamps, time.Now())
		if len(timestamps) < 4 {
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprintf(w, `{"Error":[{"error_description":"Too many requests"}]}`)
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"Response":[{"Id":{"id":1}}]}`)
	}))
	defer srv.Close()

	c := &Client{
		httpClient: srv.Client(),
		baseURL:    srv.URL,
	}

	_, _, err := c.request(context.Background(), http.MethodGet, "test", nil, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify exponential backoff: gaps should roughly double (1s, 2s, 4s)
	for i := 1; i < len(timestamps); i++ {
		gap := timestamps[i].Sub(timestamps[i-1])
		expected := time.Second << (i - 1)
		if gap < expected/2 || gap > expected*2 {
			t.Errorf("gap %d: got %v, expected ~%v", i, gap, expected)
		}
	}
}
