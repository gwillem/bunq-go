# bunq-go

[![Go Reference](https://pkg.go.dev/badge/github.com/gwillem/bunq-go.svg)](https://pkg.go.dev/github.com/gwillem/bunq-go)

Go SDK for the [bunq banking API](https://doc.bunq.com/api-reference/start-here). Generated from the official Python SDK with full endpoint coverage (165 services, 477 methods).

## Install

```
go get github.com/gwillem/bunq-go
```

## Usage

```go
package main

import (
	"context"
	"fmt"
	"log"

	bunq "github.com/gwillem/bunq-go"
)

func main() {
	ctx := context.Background()

	client, err := bunq.NewClient(ctx, bunq.Config{
		APIKey:      "your-api-key",
		Environment: bunq.Production, // or bunq.Sandbox
	})
	if err != nil {
		log.Fatal(err)
	}

	// List accounts (auto-paginates)
	for a, err := range client.MonetaryAccount.List(ctx, nil) {
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("Account %d: %s %s\n", a.ID, a.Balance.Value, a.Balance.Currency)
	}

	// Show last 5 transactions (auto-paginates)
	for p, err := range client.Payment.List(ctx, 0, &bunq.ListOptions{Count: 5}) {
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%s | %s %s | %s\n", p.Created, p.Amount.Value, p.Amount.Currency, p.Description)
	}

	// Create a payment
	id, err := client.Payment.Create(ctx, 0, bunq.PaymentCreateParams{
		Amount:            bunq.NewAmount(1.00, "EUR"),
		CounterpartyAlias: &bunq.Pointer{Type: "IBAN", Value: "NL02BUNQ0000000000", Name: "Recipient"},
		Description:       "Test payment",
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Created payment %d\n", id)
}
```

Pass `0` as the monetary account ID to use your primary account.

## Sandbox testing

```go
// Create a sandbox API key (no auth needed)
apiKey, _ := bunq.CreateSandboxAPIKey()

client, _ := bunq.NewClient(ctx, bunq.Config{
    APIKey:      apiKey,
    Environment: bunq.Sandbox,
})

// Fund the sandbox account via sugar daddy
client.RequestInquiry.Create(ctx, 0, bunq.RequestInquiryCreateParams{
    AmountInquired:    bunq.NewAmount(500, "EUR"),
    CounterpartyAlias: &bunq.Pointer{Type: "EMAIL", Value: "sugardaddy@bunq.com"},
    Description:       "top up",
    RequireAddress:    "NONE",
    AllowBunqme:       boolPtr(false),
})
```

## Error handling

```go
payment, err := client.Payment.Get(ctx, 0, 12345)
if err != nil {
    var notFound *bunq.NotFoundError
    if errors.As(err, &notFound) {
        fmt.Println("Payment not found")
    }
}
```

## Code generation

The endpoint types and services are generated from the Python SDK source:

```
go run ./cmd/generate
```
