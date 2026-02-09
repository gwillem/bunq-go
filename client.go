package bunq

import (
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const userAgent = "bunq-go/1.0.0"

// Client is the bunq API client. Create one with NewClient.
type Client struct {
	cfg        Config
	httpClient *http.Client
	baseURL    string

	privateKey      *rsa.PrivateKey
	serverPublicKey *rsa.PublicKey

	installationToken string
	sessionToken      string
	sessionExpiry     time.Time

	userID                   int
	primaryMonetaryAccountID int

	mu sync.RWMutex

	common service

	// ServiceContainer embeds all generated service accessors (e.g. client.Payment, client.Card, etc.)
	ServiceContainer
}

type service struct {
	client *Client
}

// resolveMonetaryAccountID returns the given ID, or the primary account if 0.
func (c *Client) resolveMonetaryAccountID(id int) int {
	if id == 0 {
		return c.primaryMonetaryAccountID
	}
	return id
}

// request performs an authenticated HTTP request.
func (c *Client) request(ctx context.Context, method, path string, body any, useSessionToken bool) ([]byte, http.Header, error) {
	if useSessionToken {
		if err := c.ensureSessionActive(ctx); err != nil {
			return nil, nil, err
		}
	}

	// Snapshot session fields for concurrent safety.
	// When useSessionToken=true, other goroutines may be refreshing the session,
	// so we read under RLock. When false, we're in a bootstrap path (NewClient
	// or inside ensureSessionActive's write lock), so no lock is needed.
	var token string
	var privateKey *rsa.PrivateKey
	var serverPubKey *rsa.PublicKey
	if useSessionToken {
		c.mu.RLock()
		token = c.sessionToken
		privateKey = c.privateKey
		serverPubKey = c.serverPublicKey
		c.mu.RUnlock()
	} else {
		token = c.installationToken
		privateKey = c.privateKey
		serverPubKey = c.serverPublicKey
	}

	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return nil, nil, fmt.Errorf("marshaling request body: %w", err)
		}
	}

	url := c.baseURL + "/" + path
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, nil, fmt.Errorf("creating request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("X-Bunq-Client-Request-Id", uuid.New().String())
	req.Header.Set("X-Bunq-Geolocation", "0 0 0 0 NL")
	req.Header.Set("X-Bunq-Language", "en_US")
	req.Header.Set("X-Bunq-Region", "nl_NL")
	req.Header.Set("Cache-Control", "no-cache")

	if token != "" {
		req.Header.Set("X-Bunq-Client-Authentication", token)
	}

	// Sign request body
	if privateKey != nil && token != "" {
		sig, err := signRequest(privateKey, bodyBytes)
		if err != nil {
			return nil, nil, err
		}
		req.Header.Set("X-Bunq-Client-Signature", sig)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("reading response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		responseID := resp.Header.Get("X-Bunq-Client-Response-Id")
		return nil, nil, newAPIError(resp.StatusCode, responseID, respBody)
	}

	// Verify server signature if we have the server public key
	if serverPubKey != nil {
		serverSig := resp.Header.Get("X-Bunq-Server-Signature")
		if serverSig != "" {
			if err := verifyResponse(serverPubKey, respBody, serverSig); err != nil {
				return nil, nil, fmt.Errorf("server signature verification failed: %w", err)
			}
		}
	}

	return respBody, resp.Header, nil
}

func (c *Client) get(ctx context.Context, path string, params map[string]string) ([]byte, http.Header, error) {
	if len(params) > 0 {
		v := make(url.Values, len(params))
		for key, val := range params {
			v.Set(key, val)
		}
		path += "?" + v.Encode()
	}
	return c.request(ctx, http.MethodGet, path, nil, true)
}

func (c *Client) post(ctx context.Context, path string, body any) ([]byte, http.Header, error) {
	return c.request(ctx, http.MethodPost, path, body, true)
}

func (c *Client) put(ctx context.Context, path string, body any) ([]byte, http.Header, error) {
	return c.request(ctx, http.MethodPut, path, body, true)
}

func (c *Client) delete(ctx context.Context, path string) error {
	_, _, err := c.request(ctx, http.MethodDelete, path, nil, true)
	return err
}

// unmarshalID extracts an ID from a bunq response: {"Response":[{"Id":{"id":N}}]}
func unmarshalID(body []byte) (int, error) {
	var envelope struct {
		Response []json.RawMessage `json:"Response"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return 0, fmt.Errorf("unmarshaling response envelope: %w", err)
	}
	if len(envelope.Response) == 0 {
		return 0, fmt.Errorf("empty response array")
	}

	var wrapper struct {
		ID struct {
			ID int `json:"id"`
		} `json:"Id"`
	}
	if err := json.Unmarshal(envelope.Response[0], &wrapper); err != nil {
		return 0, fmt.Errorf("unmarshaling ID wrapper: %w", err)
	}
	return wrapper.ID.ID, nil
}

// unmarshalUUID extracts a UUID from a bunq response: {"Response":[{"Uuid":{"uuid":"..."}}]}
func unmarshalUUID(body []byte) (string, error) {
	var envelope struct {
		Response []json.RawMessage `json:"Response"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return "", fmt.Errorf("unmarshaling response envelope: %w", err)
	}
	if len(envelope.Response) == 0 {
		return "", fmt.Errorf("empty response array")
	}

	var wrapper struct {
		UUID struct {
			UUID string `json:"uuid"`
		} `json:"Uuid"`
	}
	if err := json.Unmarshal(envelope.Response[0], &wrapper); err != nil {
		return "", fmt.Errorf("unmarshaling UUID wrapper: %w", err)
	}
	return wrapper.UUID.UUID, nil
}

// unmarshalObject extracts a single object from the response envelope.
func unmarshalObject[T any](body []byte, key string) (*T, error) {
	var envelope struct {
		Response []json.RawMessage `json:"Response"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("unmarshaling response envelope: %w", err)
	}
	if len(envelope.Response) == 0 {
		return nil, fmt.Errorf("empty response array")
	}

	// Unwrap: {"Key": {...}}
	var outer map[string]json.RawMessage
	if err := json.Unmarshal(envelope.Response[0], &outer); err != nil {
		return nil, fmt.Errorf("unmarshaling response item: %w", err)
	}

	inner, ok := outer[key]
	if !ok {
		// Try to find a key that starts with the given key (for anchor objects)
		for k, v := range outer {
			if strings.HasPrefix(k, key) {
				inner = v
				ok = true
				break
			}
		}
		if !ok {
			return nil, fmt.Errorf("key %q not found in response", key)
		}
	}

	var result T
	if err := json.Unmarshal(inner, &result); err != nil {
		return nil, fmt.Errorf("unmarshaling %s: %w", key, err)
	}
	return &result, nil
}

// unmarshalList extracts a list of objects from the response envelope.
func unmarshalList[T any](body []byte, key string) (*ListResponse[T], error) {
	var envelope struct {
		Response   []json.RawMessage `json:"Response"`
		Pagination *Pagination       `json:"Pagination"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("unmarshaling response envelope: %w", err)
	}

	items := make([]T, 0, len(envelope.Response))
	for _, raw := range envelope.Response {
		var outer map[string]json.RawMessage
		if err := json.Unmarshal(raw, &outer); err != nil {
			return nil, fmt.Errorf("unmarshaling list item: %w", err)
		}

		inner, ok := outer[key]
		if !ok {
			// Try prefix match
			for k, v := range outer {
				if strings.HasPrefix(k, key) {
					inner = v
					ok = true
					break
				}
			}
			if !ok {
				continue
			}
		}

		var item T
		if err := json.Unmarshal(inner, &item); err != nil {
			return nil, fmt.Errorf("unmarshaling list item %s: %w", key, err)
		}
		items = append(items, item)
	}

	return &ListResponse[T]{
		Items:      items,
		Pagination: envelope.Pagination,
	}, nil
}
