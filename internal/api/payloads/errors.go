package payloads

import (
	"encoding/json"
	"net/http"

	"github.com/TheFranconianCoder/thought-keeper/internal/types"
)

// ErrorBody is the single wire error shape.
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// HTTPStatus maps a coded error to an HTTP status code.
func HTTPStatus(err error) int {
	switch types.CodeOf(err) {
	case "not_found":
		return http.StatusNotFound
	case "conflict", "locked", "dependent_source":
		return http.StatusConflict
	case "invalid_id", "invalid_hash", "invalid_url", "invalid_tags", "unsupported_source",
		"invalid_body", "unknown_action":
		return http.StatusBadRequest
	case "unparsed":
		// 422: the request was understood as a request, just not as anything this app can do. It is
		// the honest answer to a sentence that names no lens and no criterion.
		return http.StatusUnprocessableEntity
	case "not_implemented":
		return http.StatusNotImplemented
	case "forbidden":
		return http.StatusForbidden
	case "fetch_failed", "research_failed":
		return http.StatusBadGateway
	default:
		return http.StatusInternalServerError
	}
}

// WriteJSON writes v as JSON with the given status.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError writes the error body once, centrally.
func WriteError(w http.ResponseWriter, err error) {
	WriteJSON(w, HTTPStatus(err), ErrorBody{Error: ErrorDetail{
		Code:    types.CodeOf(err),
		Message: err.Error(),
	}})
}
