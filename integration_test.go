//go:build integration

package bunq

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

func boolPtr(b bool) *bool { return &b }

func TestIntegration(t *testing.T) {
	ctx := context.Background()

	apiKey := os.Getenv("BUNQ_API_KEY")
	if apiKey == "" {
		t.Log("No BUNQ_API_KEY set, creating sandbox user...")
		var err error
		apiKey, err = CreateSandboxAPIKey()
		if err != nil {
			t.Fatalf("creating sandbox API key: %v", err)
		}
		t.Logf("Created sandbox API key: %s...", apiKey[:min(len(apiKey), 8)])
	}

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
		resp, err := client.MonetaryAccount.List(ctx, nil)
		if err != nil {
			t.Fatalf("listing monetary accounts: %v", err)
		}
		if len(resp.Items) == 0 {
			t.Fatal("expected at least one monetary account")
		}
		t.Logf("Found %d monetary accounts", len(resp.Items))
	})

	t.Run("RequestMoney", func(t *testing.T) {
		// Request money from the sandbox sugar daddy to fund the account
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
		// Wait for sugar daddy to process the request
		time.Sleep(2 * time.Second)

		// Create a payment (account should now be funded by sugar daddy)
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

		// Get the payment by ID
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
		resp, err := client.Payment.List(ctx, 0, &ListOptions{Count: 5})
		if err != nil {
			t.Fatalf("listing payments: %v", err)
		}
		t.Logf("Found %d payments (count=5)", len(resp.Items))
		if resp.Pagination != nil {
			t.Logf("Pagination: older_id=%v, newer_id=%v", resp.Pagination.OlderID, resp.Pagination.NewerID)
		}
	})

	t.Run("ListCards", func(t *testing.T) {
		resp, err := client.Card.List(ctx, nil)
		if err != nil {
			t.Fatalf("listing cards: %v", err)
		}
		t.Logf("Found %d cards", len(resp.Items))
	})
}
