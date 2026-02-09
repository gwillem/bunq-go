package bunq

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
)

// FlexFloat64 is a float64 that can be unmarshaled from both JSON numbers and strings.
// The bunq API returns some numeric fields (e.g. savings_goal_progress) as strings.
// See: json: cannot unmarshal string into Go struct field MonetaryAccountSavings.savings_goal_progress of type float64
type FlexFloat64 float64

func (f *FlexFloat64) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return nil
	}
	// Try number first
	var n float64
	if err := json.Unmarshal(data, &n); err == nil {
		*f = FlexFloat64(n)
		return nil
	}
	// Try string
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("FlexFloat64: cannot unmarshal %s", data)
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("FlexFloat64: cannot parse %q: %w", s, err)
	}
	*f = FlexFloat64(n)
	return nil
}

func (f FlexFloat64) MarshalJSON() ([]byte, error) {
	return json.Marshal(float64(f))
}

// NewAmount creates an Amount from a float64 value and currency code.
func NewAmount(value float64, currency string) *Amount {
	return &Amount{
		Value:    strconv.FormatFloat(value, 'f', 2, 64),
		Currency: currency,
	}
}

// Float64 returns the Amount's value as a float64.
// Returns 0 if the value cannot be parsed.
func (a *Amount) Float64() float64 {
	n, _ := strconv.ParseFloat(a.Value, 64)
	return n
}

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
		p["count"] = fmt.Sprintf("%d", o.Count)
	}
	if o.OlderID > 0 {
		p["older_id"] = fmt.Sprintf("%d", o.OlderID)
	}
	if o.NewerID > 0 {
		p["newer_id"] = fmt.Sprintf("%d", o.NewerID)
	}
	if len(p) == 0 {
		return nil
	}
	return p
}
