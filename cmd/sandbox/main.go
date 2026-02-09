package main

import (
	"context"
	"fmt"
	"log"
	"time"

	bunq "github.com/gwillem/bunq-go"
)

func boolPtr(b bool) *bool { return &b }

func main() {
	ctx := context.Background()

	// 1. Create sandbox user
	fmt.Println("=== Creating sandbox user ===")
	apiKey, err := bunq.CreateSandboxAPIKey()
	if err != nil {
		log.Fatalf("Creating sandbox API key: %v", err)
	}
	fmt.Printf("  API key: %s...\n", apiKey[:min(len(apiKey), 16)])

	// 2. Connect client
	fmt.Println("\n=== Connecting client ===")
	fmt.Println("  Generating RSA keypair...")
	fmt.Println("  POST /installation")
	fmt.Println("  POST /device-server")
	fmt.Println("  POST /session-server")
	fmt.Println("  GET  /user/{id}/monetary-account")

	client, err := bunq.NewClient(ctx, bunq.Config{
		APIKey:      apiKey,
		Environment: bunq.Sandbox,
		Description: "bunq-go-sandbox-demo",
	})
	if err != nil {
		log.Fatalf("Creating client: %v", err)
	}
	fmt.Printf("  User ID: %d\n", client.UserID())
	fmt.Printf("  Primary account ID: %d\n", client.PrimaryMonetaryAccountID())

	// 3. List accounts
	fmt.Println("\n=== Listing monetary accounts ===")
	accounts, err := client.MonetaryAccountBank.List(ctx, nil)
	if err != nil {
		log.Fatalf("Listing accounts: %v", err)
	}
	for _, a := range accounts.Items {
		balance := "n/a"
		if a.Balance != nil {
			balance = a.Balance.Value + " " + a.Balance.Currency
		}
		fmt.Printf("  Account %d: %s (status: %s, balance: %s)\n", a.ID, a.Description, a.Status, balance)
	}

	// 4. Fund account via sugar daddy
	fmt.Println("\n=== Requesting funds from sugar daddy ===")
	reqID, err := client.RequestInquiry.Create(ctx, 0, bunq.RequestInquiryCreateParams{
		AmountInquired: &bunq.Amount{
			Value:    "500.00",
			Currency: "EUR",
		},
		CounterpartyAlias: &bunq.Pointer{
			Type:  "EMAIL",
			Value: "sugardaddy@bunq.com",
		},
		Description:    "fund sandbox account",
		AllowBunqme:    boolPtr(false),
		RequireAddress: "NONE",
	})
	if err != nil {
		log.Fatalf("Requesting funds: %v", err)
	}
	fmt.Printf("  Request inquiry ID: %d\n", reqID)
	fmt.Println("  Waiting for sugar daddy to process...")
	time.Sleep(3 * time.Second)

	// 5. Check balance after funding
	fmt.Println("\n=== Checking balance after funding ===")
	accounts, err = client.MonetaryAccountBank.List(ctx, nil)
	if err != nil {
		log.Fatalf("Listing accounts: %v", err)
	}
	for _, a := range accounts.Items {
		balance := "n/a"
		if a.Balance != nil {
			balance = a.Balance.Value + " " + a.Balance.Currency
		}
		fmt.Printf("  Account %d: %s (status: %s, balance: %s)\n", a.ID, a.Description, a.Status, balance)
	}

	// 6. Create a payment
	fmt.Println("\n=== Creating payment ===")
	paymentID, err := client.Payment.Create(ctx, 0, bunq.PaymentCreateParams{
		Amount: &bunq.Amount{
			Value:    "12.50",
			Currency: "EUR",
		},
		CounterpartyAlias: &bunq.Pointer{
			Type:  "EMAIL",
			Value: "sugardaddy@bunq.com",
		},
		Description: "Lunch money",
	})
	if err != nil {
		log.Fatalf("Creating payment: %v", err)
	}
	fmt.Printf("  Payment ID: %d\n", paymentID)

	// 7. Get payment details
	fmt.Println("\n=== Getting payment details ===")
	payment, err := client.Payment.Get(ctx, 0, paymentID)
	if err != nil {
		log.Fatalf("Getting payment: %v", err)
	}
	fmt.Printf("  ID:          %d\n", payment.ID)
	fmt.Printf("  Amount:      %s %s\n", payment.Amount.Value, payment.Amount.Currency)
	fmt.Printf("  Description: %s\n", payment.Description)
	fmt.Printf("  Type:        %s / %s\n", payment.Type, payment.SubType)
	fmt.Printf("  Created:     %s\n", payment.Created)
	if payment.BalanceAfterMutation != nil {
		fmt.Printf("  Balance:     %s %s\n", payment.BalanceAfterMutation.Value, payment.BalanceAfterMutation.Currency)
	}

	// 8. List last 5 payments
	fmt.Println("\n=== Last 5 payments ===")
	payments, err := client.Payment.List(ctx, 0, &bunq.ListOptions{Count: 5})
	if err != nil {
		log.Fatalf("Listing payments: %v", err)
	}
	for i, p := range payments.Items {
		fmt.Printf("  %d. [%d] %s %s - %s (%s)\n",
			i+1, p.ID, p.Amount.Value, p.Amount.Currency, p.Description, p.Created)
	}
	if payments.Pagination != nil {
		fmt.Printf("  Pagination: older_id=%v newer_id=%v\n",
			payments.Pagination.OlderID, payments.Pagination.NewerID)
	}

	// 9. List cards
	fmt.Println("\n=== Listing cards ===")
	cards, err := client.Card.List(ctx, nil)
	if err != nil {
		log.Fatalf("Listing cards: %v", err)
	}
	if len(cards.Items) == 0 {
		fmt.Println("  No cards found")
	}
	for _, c := range cards.Items {
		fmt.Printf("  Card %d: %s %s (status: %s)\n", c.ID, c.Type, c.SubType, c.Status)
	}

	fmt.Println("\n=== Done ===")
}
