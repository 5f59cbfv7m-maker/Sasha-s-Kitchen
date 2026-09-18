// Package httpx holds transport-level helpers: error mapping, JSON responses
// and middleware. Nothing in here knows about the recipe domain.
package httpx

import (
	"errors"
	"fmt"
	"net/http"
)

// Code is a stable, machine-readable error identifier. Clients branch on this,
// never on the human-readable message, so these strings are part of the API
// contract and must not be renamed casually.
type Code string

const (
	CodeBadRequest    Code = "bad_request"
	CodeValidation    Code = "validation_failed"
	CodeUnauthorized  Code = "unauthorized"
	CodeForbidden     Code = "forbidden"
	CodeNotFound      Code = "not_found"
	CodeConflict      Code = "conflict"
	CodeGone          Code = "gone"
	CodeRateLimited   Code = "rate_limited"
	CodePayloadTooBig Code = "payload_too_large"
	CodeInternal      Code = "internal_error"
	CodeUnavailable   Code = "service_unavailable"
)

// Error is an API-level error carrying everything needed to render a response.
type Error struct {
	Code    Code              `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
	status  int
	cause   error
}

func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.cause }

// Status returns the HTTP status this error maps to.
func (e *Error) Status() int {
	if e.status == 0 {
		return http.StatusInternalServerError
	}
	return e.status
}

// WithCause attaches an internal cause. The cause is logged, never serialized.
func (e *Error) WithCause(err error) *Error {
	clone := *e
	clone.cause = err
	return &clone
}

func newError(status int, code Code, msg string) *Error {
	return &Error{Code: code, Message: msg, status: status}
}

func BadRequest(msg string) *Error   { return newError(http.StatusBadRequest, CodeBadRequest, msg) }
func Unauthorized(msg string) *Error { return newError(http.StatusUnauthorized, CodeUnauthorized, msg) }
func Forbidden(msg string) *Error    { return newError(http.StatusForbidden, CodeForbidden, msg) }
func NotFound(msg string) *Error     { return newError(http.StatusNotFound, CodeNotFound, msg) }
func Conflict(msg string) *Error     { return newError(http.StatusConflict, CodeConflict, msg) }
func Gone(msg string) *Error         { return newError(http.StatusGone, CodeGone, msg) }
func RateLimited(msg string) *Error {
	return newError(http.StatusTooManyRequests, CodeRateLimited, msg)
}
func Internal(msg string) *Error { return newError(http.StatusInternalServerError, CodeInternal, msg) }
func Unavailable(msg string) *Error {
	return newError(http.StatusServiceUnavailable, CodeUnavailable, msg)
}

// Validation builds a 422 carrying per-field messages.
func Validation(fields map[string]string) *Error {
	e := newError(http.StatusUnprocessableEntity, CodeValidation, "Проверьте заполненные поля")
	e.Fields = fields
	return e
}

// AsError maps any error to an *Error, defaulting to a 500 that hides internals.
func AsError(err error) *Error {
	var apiErr *Error
	if errors.As(err, &apiErr) {
		return apiErr
	}
	return Internal("Внутренняя ошибка сервера").WithCause(err)
}
