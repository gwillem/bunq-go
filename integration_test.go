//go:build integration

package bunq

import (
	"context"
	"errors"
	"testing"
	"time"
)

func boolPtr(b bool) *bool { return &b }

func TestIntegration(t *testing.T) {
	ctx := context.Background()

	apiKey, err := CreateSandboxAPIKey()
	if err != nil {
		t.Fatalf("creating sandbox API key: %v", err)
	}
	t.Logf("Created sandbox API key: %s...", apiKey[:min(len(apiKey), 8)])

	client, err := NewClient(ctx, Config{
		APIKey:      apiKey,
		Environment: Sandbox,
		Description: "bunq-go-integration-test",
	})
	if err != nil {
		t.Fatalf("creating client: %v", err)
	}

	t.Logf("User ID: %d", client.UserID())
	t.Logf("Primary Account ID: %d", client.PrimaryMonetaryAccountID())

	t.Run("ListMonetaryAccounts", func(t *testing.T) {
		count := 0
		for _, err := range client.MonetaryAccount.List(ctx, nil) {
			if err != nil {
				t.Fatalf("listing monetary accounts: %v", err)
			}
			count++
		}
		if count == 0 {
			t.Fatal("expected at least one monetary account")
		}
		t.Logf("Found %d monetary accounts", count)
	})

	t.Run("RequestMoney", func(t *testing.T) {
		reqID, err := client.RequestInquiry.Create(ctx, 0, RequestInquiryCreateParams{
			AmountInquired: NewAmount(100, "EUR"),
			CounterpartyAlias: &Pointer{
				Type:  "EMAIL",
				Value: "sugardaddy@bunq.com",
			},
			Description:    "fund test account",
			AllowBunqme:    boolPtr(false),
			RequireAddress: "NONE",
		})
		if err != nil {
			t.Fatalf("requesting money: %v", err)
		}
		t.Logf("Created request inquiry with ID: %d", reqID)
	})

	t.Run("CreatePayment", func(t *testing.T) {
		time.Sleep(2 * time.Second)

		var paymentID int
		var err error
		for range 3 {
			paymentID, err = client.Payment.Create(ctx, 0, PaymentCreateParams{
				Amount: NewAmount(0.01, "EUR"),
				CounterpartyAlias: &Pointer{
					Type:  "EMAIL",
					Value: "sugardaddy@bunq.com",
				},
				Description: "bunq-go integration test",
			})
			if err == nil {
				break
			}
			var badReq *BadRequestError
			if errors.As(err, &badReq) {
				t.Logf("Payment failed (retrying): %v", err)
				time.Sleep(2 * time.Second)
				continue
			}
			break
		}
		if err != nil {
			t.Fatalf("creating payment: %v", err)
		}
		t.Logf("Created payment with ID: %d", paymentID)

		t.Run("GetPayment", func(t *testing.T) {
			payment, err := client.Payment.Get(ctx, 0, paymentID)
			if err != nil {
				t.Fatalf("getting payment: %v", err)
			}
			if payment.ID != paymentID {
				t.Errorf("expected payment ID %d, got %d", paymentID, payment.ID)
			}
			if payment.Description != "bunq-go integration test" {
				t.Errorf("expected description 'bunq-go integration test', got %q", payment.Description)
			}
			t.Logf("Payment amount: %s %s", payment.Amount.Value, payment.Amount.Currency)
		})
	})

	t.Run("ListPayments", func(t *testing.T) {
		count := 0
		for p, err := range client.Payment.List(ctx, 0, &ListOptions{Count: 5}) {
			if err != nil {
				t.Fatalf("listing payments: %v", err)
			}
			count++
			t.Logf("  Payment %d: %s %s - %s", p.ID, p.Amount.Value, p.Amount.Currency, p.Description)
		}
		t.Logf("Found %d payments (count=5)", count)
	})

	t.Run("ListCards", func(t *testing.T) {
		count := 0
		for _, err := range client.Card.List(ctx, nil) {
			if err != nil {
				t.Fatalf("listing cards: %v", err)
			}
			count++
		}
		t.Logf("Found %d cards", count)
	})

	// Pagination: the CreatePayment + RequestMoney above guarantee >=2 payments.
	// List with Count:1 to force multiple pages.
	t.Run("Pagination", func(t *testing.T) {
		total := 0
		for _, err := range client.Payment.List(ctx, 0, &ListOptions{Count: 1}) {
			if err != nil {
				t.Fatalf("listing payments: %v", err)
			}
			total++
		}
		t.Logf("Listed %d total payments across pages", total)
		if total < 2 {
			t.Errorf("expected at least 2 payments, got %d", total)
		}
	})
}
