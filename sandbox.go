package bunq

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// CreateSandboxAPIKey creates a new sandbox user and returns its API key.
// This calls the sandbox API directly without authentication.
func CreateSandboxAPIKey() (string, error) {
	url := Sandbox.BaseURL + "/sandbox-user-person"

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader([]byte("{}")))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("X-Bunq-Client-Request-Id", "sandbox-setup")
	req.Header.Set("X-Bunq-Geolocation", "0 0 0 0 NL")
	req.Header.Set("X-Bunq-Language", "en_US")
	req.Header.Set("X-Bunq-Region", "nl_NL")
	req.Header.Set("Cache-Control", "no-cache")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("sandbox user creation failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Response: {"Response":[{"ApiKey":{"api_key":"..."}}]}
	var envelope struct {
		Response []json.RawMessage `json:"Response"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return "", fmt.Errorf("parsing response: %w", err)
	}

	for _, raw := range envelope.Response {
		var item map[string]json.RawMessage
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}
		if apiKeyJSON, ok := item["ApiKey"]; ok {
			var apiKey struct {
				APIKey string `json:"api_key"`
			}
			if err := json.Unmarshal(apiKeyJSON, &apiKey); err != nil {
				return "", fmt.Errorf("parsing api key: %w", err)
			}
			return apiKey.APIKey, nil
		}
	}

	return "", fmt.Errorf("no API key found in response")
}
