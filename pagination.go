package bunq

// Pagination holds cursor information returned by list endpoints.
type Pagination struct {
	OlderID  *int `json:"older_id"`
	NewerID  *int `json:"newer_id"`
	FutureID *int `json:"future_id"`
	Count    *int `json:"count"`
}

// ListResponse wraps a list of items with pagination information.
type ListResponse[T any] struct {
	Items      []T
	Pagination *Pagination
}
