package bunq

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// APIError represents an error response from the bunq API.
type APIError struct {
	StatusCode int
	ResponseID string
	Messages   []string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("bunq API error %d (response-id: %s): %s",
		e.StatusCode, e.ResponseID, strings.Join(e.Messages, "; "))
}

type BadRequestError struct{ APIError }
type UnauthorizedError struct{ APIError }
type ForbiddenError struct{ APIError }
type NotFoundError struct{ APIError }
type MethodNotAllowedError struct{ APIError }
type TooManyRequestsError struct{ APIError }
type InternalServerError struct{ APIError }

// errorResponse is the JSON envelope for bunq error responses.
type errorResponse struct {
	Error []struct {
		ErrorDescription string `json:"error_description"`
	} `json:"Error"`
}

func newAPIError(statusCode int, responseID string, body []byte) error {
	var errResp errorResponse
	messages := []string{"unknown error"}
	if err := json.Unmarshal(body, &errResp); err == nil && len(errResp.Error) > 0 {
		messages = make([]string, len(errResp.Error))
		for i, e := range errResp.Error {
			messages[i] = e.ErrorDescription
		}
	}

	base := APIError{
		StatusCode: statusCode,
		ResponseID: responseID,
		Messages:   messages,
	}

	switch statusCode {
	case http.StatusBadRequest:
		return &BadRequestError{base}
	case http.StatusUnauthorized:
		return &UnauthorizedError{base}
	case http.StatusForbidden:
		return &ForbiddenError{base}
	case http.StatusNotFound:
		return &NotFoundError{base}
	case http.StatusMethodNotAllowed:
		return &MethodNotAllowedError{base}
	case http.StatusTooManyRequests:
		return &TooManyRequestsError{base}
	case http.StatusInternalServerError:
		return &InternalServerError{base}
	default:
		return &base
	}
}
