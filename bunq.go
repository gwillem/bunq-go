package bunq

import (
	"fmt"
	"net/http"
)

// Environment represents a bunq API environment (production or sandbox).
type Environment struct {
	BaseURL string
}

var (
	Production = Environment{BaseURL: "https://api.bunq.com/v1"}
	Sandbox    = Environment{BaseURL: "https://public-api.sandbox.bunq.com/v1"}
)

// Config holds configuration for creating a new Client.
type Config struct {
	APIKey      string
	Environment Environment
	Description string       // device description, defaults to "bunq-go"
	AllowedIPs  []string     // empty = wildcard (*)
	HTTPClient  *http.Client // optional, defaults to http.DefaultClient
}

// ListOptions controls pagination for list endpoints.
type ListOptions struct {
	Count   int
	OlderID int
	NewerID int
}

func (o *ListOptions) toParams() map[string]string {
	if o == nil {
		return nil
	}
	p := map[string]string{}
	if o.Count > 0 {
		p["count"] = intToStr(o.Count)
	}
	if o.OlderID > 0 {
		p["older_id"] = intToStr(o.OlderID)
	}
	if o.NewerID > 0 {
		p["newer_id"] = intToStr(o.NewerID)
	}
	if len(p) == 0 {
		return nil
	}
	return p
}

func intToStr(i int) string {
	return fmt.Sprintf("%d", i)
}
