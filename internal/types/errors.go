// Package types holds sentinel errors and the error type that maps to HTTP.
package types

import (
	"errors"
	"fmt"
)

// Sentinel errors. Handlers translate these to HTTP once, centrally in internal/api.
var (
	ErrNotFound          = errors.New("not found")
	ErrConflict          = errors.New("conflict")
	ErrInvalidID         = errors.New("invalid id")
	ErrInvalidHash       = errors.New("invalid hash")
	ErrInvalidURL        = errors.New("invalid url")
	ErrInvalidTags       = errors.New("invalid tags")
	ErrUnsupportedSource = errors.New("unsupported source")
	ErrLocked            = errors.New("vault locked")
	ErrNoInstance        = errors.New("no running instance")
	ErrNotImplemented    = errors.New("not implemented")
	ErrForbidden         = errors.New("forbidden")
	ErrInternal          = errors.New("internal error")
)

// Error is a coded error carrying a stable machine code and a human message.
type Error struct {
	Code    string
	Message string
	err     error
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.err.Error()
}
func (e *Error) Unwrap() error { return e.err }

// New builds a coded error.
func New(code, message string, err error) *Error {
	return &Error{Code: code, Message: message, err: err}
}

// CodeOf returns the machine code for an error, defaulting to "internal".
func CodeOf(err error) string {
	if e, ok := errors.AsType[*Error](err); ok {
		return e.Code
	}
	switch {
	case errors.Is(err, ErrNotFound):
		return "not_found"
	case errors.Is(err, ErrConflict):
		return "conflict"
	case errors.Is(err, ErrInvalidID):
		return "invalid_id"
	case errors.Is(err, ErrInvalidHash):
		return "invalid_hash"
	case errors.Is(err, ErrInvalidURL):
		return "invalid_url"
	case errors.Is(err, ErrInvalidTags):
		return "invalid_tags"
	case errors.Is(err, ErrUnsupportedSource):
		return "unsupported_source"
	case errors.Is(err, ErrLocked):
		return "locked"
	case errors.Is(err, ErrNoInstance):
		return "no_instance"
	case errors.Is(err, ErrNotImplemented):
		return "not_implemented"
	case errors.Is(err, ErrForbidden):
		return "forbidden"
	default:
		return "internal"
	}
}

// Wrapf is a small helper for context.
func Wrapf(err error, format string, args ...any) error {
	return fmt.Errorf(format+": %w", append(args, err)...)
}
