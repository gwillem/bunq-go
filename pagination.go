package bunq

import (
	"context"
	"fmt"
	"iter"
	"net/url"
	"strconv"
)

// Pagination holds cursor information returned by list endpoints.
// The bunq API returns URLs (older_url, newer_url, future_url) from which
// integer IDs can be extracted.
type Pagination struct {
	OlderURL  string `json:"older_url"`
	NewerURL  string `json:"newer_url"`
	FutureURL string `json:"future_url"`
}

// olderID extracts the older_id query parameter from OlderURL.
func (p *Pagination) olderID() (int, bool) {
	if p == nil {
		return 0, false
	}
	return parseIDFromURL(p.OlderURL, "older_id")
}

// newerID extracts the newer_id query parameter from NewerURL.
func (p *Pagination) newerID() (int, bool) {
	if p == nil {
		return 0, false
	}
	return parseIDFromURL(p.NewerURL, "newer_id")
}

func parseIDFromURL(rawURL, param string) (int, bool) {
	if rawURL == "" {
		return 0, false
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return 0, false
	}
	s := u.Query().Get(param)
	if s == "" {
		return 0, false
	}
	id, err := strconv.Atoi(s)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// listResponse wraps a list of items with pagination information.
type listResponse[T any] struct {
	Items      []T
	Pagination *Pagination
}

// defaultListCount is the default number of items per page. The bunq API
// maximum is 200; using it minimizes the number of requests and avoids
// hitting rate limits (3 GET calls per 3 seconds).
const defaultListCount = 200

// listIter returns an iterator that automatically paginates through all items.
func listIter[T any](c *Client, ctx context.Context, path, key string, opts *ListOptions) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		count := defaultListCount
		if opts != nil && opts.Count > 0 {
			count = opts.Count
		}
		if opts == nil {
			opts = &ListOptions{}
		}
		if opts.Count == 0 {
			opts.Count = count
		}
		params := opts.toParams()
		prevOlderID := 0
		for {
			body, _, err := c.get(ctx, path, params)
			if err != nil {
				var zero T
				yield(zero, fmt.Errorf("listing %s: %w", key, err))
				return
			}
			resp, err := unmarshalList[T](body, key)
			if err != nil {
				var zero T
				yield(zero, fmt.Errorf("unmarshaling %s list: %w", key, err))
				return
			}
			if len(resp.Items) == 0 {
				return
			}
			for _, item := range resp.Items {
				if !yield(item, nil) {
					return
				}
			}
			olderID, ok := resp.Pagination.olderID()
			if !ok || olderID == prevOlderID {
				return
			}
			prevOlderID = olderID
			params = (&ListOptions{OlderID: olderID, Count: count}).toParams()
		}
	}
}
