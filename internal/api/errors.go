package api

import (
	"errors"
	"fmt"
	"net/http"
)

// Sentinel errors for the contract's error statuses. Match them with errors.Is.
var (
	ErrUnauthorized = errors.New("unauthorized: the API key is missing, invalid or revoked")
	ErrForbidden    = errors.New("forbidden: the API key lacks the scope for this call")
	ErrNotFound     = errors.New("not found")
	ErrConflict     = errors.New("conflict")
	ErrValidation   = errors.New("invalid request")
)

// Error is a contract-shaped API error: {"error":{"code":"…","message":"…"}}.
// errors.Is(err, ErrNotFound) and friends work on it; errors.As exposes the
// status, code and the server's message.
type Error struct {
	Status  int    // HTTP status code
	Code    string // machine code, e.g. not_found, ambiguous, unknown_service
	Message string // human message from the server
	Method  string
	Path    string
}

func (e *Error) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	if e.Code != "" {
		return fmt.Sprintf("%s %s: %s (%s)", e.Method, e.Path, msg, e.Code)
	}
	return fmt.Sprintf("%s %s: %s", e.Method, e.Path, msg)
}

// Is maps the HTTP status to the sentinel errors.
func (e *Error) Is(target error) bool {
	switch target {
	case ErrUnauthorized:
		return e.Status == http.StatusUnauthorized
	case ErrForbidden:
		return e.Status == http.StatusForbidden
	case ErrNotFound:
		return e.Status == http.StatusNotFound
	case ErrConflict:
		return e.Status == http.StatusConflict
	case ErrValidation:
		return e.Status == http.StatusBadRequest || e.Status == http.StatusUnprocessableEntity
	}
	return false
}

// IsValidation reports whether err is a 400/422 and returns the server's message.
func IsValidation(err error) (string, bool) {
	var e *Error
	if errors.As(err, &e) && errors.Is(e, ErrValidation) {
		return e.Message, true
	}
	return "", false
}

// Code returns the contract error code ("ambiguous", "unknown_service", …) or "".
func Code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// TransientError wraps network failures and 5xx responses that survived every retry.
type TransientError struct {
	Err error
}

func (e *TransientError) Error() string { return "Alror workspace unreachable: " + e.Err.Error() }
func (e *TransientError) Unwrap() error { return e.Err }

// IsTransient reports whether err is a network error or 5xx (worth retrying later).
func IsTransient(err error) bool {
	var t *TransientError
	return errors.As(err, &t)
}
