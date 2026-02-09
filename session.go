package bunq

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// NewClient creates a new bunq API client. It performs the full bootstrap:
// installation → device-server → session-server → find primary account.
func NewClient(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.Description == "" {
		cfg.Description = "bunq-go"
	}
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	c := &Client{
		cfg:        cfg,
		httpClient: httpClient,
		baseURL:    cfg.Environment.BaseURL,
	}

	// 1. Generate RSA key pair
	privateKey, err := generateRSAKeyPair()
	if err != nil {
		return nil, fmt.Errorf("generating RSA key pair: %w", err)
	}
	c.privateKey = privateKey

	// 2. POST /installation
	if err := c.doInstallation(ctx); err != nil {
		return nil, fmt.Errorf("installation: %w", err)
	}

	// 3. POST /device-server
	if err := c.doDeviceServer(ctx); err != nil {
		return nil, fmt.Errorf("device-server: %w", err)
	}

	// 4. POST /session-server
	if err := c.doSessionServer(ctx); err != nil {
		return nil, fmt.Errorf("session-server: %w", err)
	}

	// 5. Find primary monetary account
	if err := c.findPrimaryAccount(ctx); err != nil {
		return nil, fmt.Errorf("finding primary account: %w", err)
	}

	// 6. Wire up services
	c.initServices()

	return c, nil
}

func (c *Client) doInstallation(ctx context.Context) error {
	reqBody := map[string]string{
		"client_public_key": publicKeyToPEM(&c.privateKey.PublicKey),
	}

	body, _, err := c.request(ctx, http.MethodPost, "installation", reqBody, false)
	if err != nil {
		return err
	}

	// Response: {"Response":[{"Id":{"id":N}},{"Token":{"token":"..."}},{"ServerPublicKey":{"server_public_key":"..."}}]}
	var envelope struct {
		Response []json.RawMessage `json:"Response"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("parsing installation response: %w", err)
	}

	for _, raw := range envelope.Response {
		var item map[string]json.RawMessage
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}

		if tokenJSON, ok := item["Token"]; ok {
			var token struct {
				Token string `json:"token"`
			}
			if err := json.Unmarshal(tokenJSON, &token); err != nil {
				return fmt.Errorf("parsing token: %w", err)
			}
			c.installationToken = token.Token
		}

		if keyJSON, ok := item["ServerPublicKey"]; ok {
			var key struct {
				ServerPublicKey string `json:"server_public_key"`
			}
			if err := json.Unmarshal(keyJSON, &key); err != nil {
				return fmt.Errorf("parsing server public key: %w", err)
			}
			pub, err := parsePublicKeyPEM(key.ServerPublicKey)
			if err != nil {
				return fmt.Errorf("parsing server public key PEM: %w", err)
			}
			c.serverPublicKey = pub
		}
	}

	if c.installationToken == "" {
		return fmt.Errorf("no installation token in response")
	}
	if c.serverPublicKey == nil {
		return fmt.Errorf("no server public key in response")
	}

	return nil
}

func (c *Client) doDeviceServer(ctx context.Context) error {
	ips := c.cfg.AllowedIPs
	if len(ips) == 0 {
		ips = []string{"*"}
	}

	reqBody := map[string]any{
		"description":  c.cfg.Description,
		"secret":       c.cfg.APIKey,
		"permitted_ips": ips,
	}

	// device-server uses installation token
	_, _, err := c.request(ctx, http.MethodPost, "device-server", reqBody, false)
	return err
}

func (c *Client) doSessionServer(ctx context.Context) error {
	reqBody := map[string]string{
		"secret": c.cfg.APIKey,
	}

	body, _, err := c.request(ctx, http.MethodPost, "session-server", reqBody, false)
	if err != nil {
		return err
	}

	return c.parseSessionResponse(body)
}

func (c *Client) parseSessionResponse(body []byte) error {
	var envelope struct {
		Response []json.RawMessage `json:"Response"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("parsing session response: %w", err)
	}

	var sessionTimeout int

	for _, raw := range envelope.Response {
		var item map[string]json.RawMessage
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}

		if tokenJSON, ok := item["Token"]; ok {
			var token struct {
				Token string `json:"token"`
			}
			if err := json.Unmarshal(tokenJSON, &token); err != nil {
				return fmt.Errorf("parsing session token: %w", err)
			}
			c.sessionToken = token.Token
		}

		// User can be UserPerson, UserCompany, UserApiKey, etc.
		for key, val := range item {
			if key == "Id" || key == "Token" {
				continue
			}
			var user struct {
				ID             int `json:"id"`
				SessionTimeout int `json:"session_timeout"`
			}
			if err := json.Unmarshal(val, &user); err == nil && user.ID > 0 {
				c.userID = user.ID
				sessionTimeout = user.SessionTimeout
			}
		}
	}

	if c.sessionToken == "" {
		return fmt.Errorf("no session token in response")
	}
	if c.userID == 0 {
		return fmt.Errorf("no user ID in response")
	}

	if sessionTimeout == 0 {
		sessionTimeout = 1800 // default 30 minutes
	}
	c.sessionExpiry = time.Now().Add(time.Duration(sessionTimeout) * time.Second)

	return nil
}

func (c *Client) findPrimaryAccount(ctx context.Context) error {
	path := fmt.Sprintf("user/%d/monetary-account", c.userID)
	body, _, err := c.get(ctx, path, nil)
	if err != nil {
		return err
	}

	var envelope struct {
		Response []json.RawMessage `json:"Response"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("parsing monetary accounts: %w", err)
	}

	for _, raw := range envelope.Response {
		var outer map[string]json.RawMessage
		if err := json.Unmarshal(raw, &outer); err != nil {
			continue
		}
		for _, val := range outer {
			var account struct {
				ID     int    `json:"id"`
				Status string `json:"status"`
			}
			if err := json.Unmarshal(val, &account); err == nil && account.Status == "ACTIVE" && account.ID > 0 {
				c.primaryMonetaryAccountID = account.ID
				return nil
			}
		}
	}

	return fmt.Errorf("no active monetary account found")
}

func (c *Client) ensureSessionActive(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if time.Until(c.sessionExpiry) > 30*time.Second {
		return nil
	}

	return c.doSessionServer(ctx)
}

// UserID returns the authenticated user's ID.
func (c *Client) UserID() int {
	return c.userID
}

// PrimaryMonetaryAccountID returns the primary monetary account ID.
func (c *Client) PrimaryMonetaryAccountID() int {
	return c.primaryMonetaryAccountID
}

// Ensure uuid is used (referenced in request headers)
var _ = uuid.New
