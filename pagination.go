package bunq

import (
	"context"
	"fmt"
	"iter"
)

// Pagination holds cursor information returned by list endpoints.
type Pagination struct {
	OlderID  *int `json:"older_id"`
	NewerID  *int `json:"newer_id"`
	FutureID *int `json:"future_id"`
	Count    *int `json:"count"`
}

// listResponse wraps a list of items with pagination information.
type listResponse[T any] struct {
	Items      []T
	Pagination *Pagination
}

// listIter returns an iterator that automatically paginates through all items.
func listIter[T any](c *Client, ctx context.Context, path, key string, opts *ListOptions) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		params := opts.toParams()
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
			for _, item := range resp.Items {
				if !yield(item, nil) {
					return
				}
			}
			if resp.Pagination == nil || resp.Pagination.OlderID == nil {
				return
			}
			params = (&ListOptions{OlderID: *resp.Pagination.OlderID}).toParams()
		}
	}
}
