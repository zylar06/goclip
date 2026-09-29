// Package ai implements bounded, non-retrying model requests and analysis.
package ai

import (
	"context"
	"errors"
	"fmt"
)

const (
	CodeAuth            = "auth"
	CodeModel           = "model"
	CodeEndpoint        = "endpoint"
	CodeTimeout         = "timeout"
	CodeRateLimit       = "rate_limit"
	CodeInvalidResponse = "invalid_response"
)

// Error contains only locally authored diagnostics. Neither provider bodies,
// credentials, prompts, URLs nor underlying network/filesystem errors are exposed.
// Retryable means an EXPLICIT user retry may help; it never schedules a request.
type Error struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Stage      string `json:"stage,omitempty"`
	HTTPStatus int    `json:"http_status,omitempty"`
	Retryable  bool   `json:"retryable"`
	cause      error
}

func (e *Error) Error() string {
	if e.Stage != "" {
		return fmt.Sprintf("ai %s (%s): %s", e.Code, e.Stage, e.Message)
	}
	return fmt.Sprintf("ai %s: %s", e.Code, e.Message)
}

// Unwrap exposes only context cancellation/deadline sentinels, never raw errors.
func (e *Error) Unwrap() error { return e.cause }

func failure(code, message string) *Error {
	return &Error{Code: code, Message: message, Retryable: code == CodeTimeout || code == CodeRateLimit}
}

func invalid(message string) *Error { return failure(CodeInvalidResponse, message) }

func contextError(ctx context.Context) error {
	if ctx.Err() == nil {
		return nil
	}
	e := failure(CodeTimeout, "Request cancelled; no automatic retry was made.")
	e.cause = ctx.Err()
	e.Retryable = !errors.Is(ctx.Err(), context.Canceled)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		e.Message = "Request deadline exceeded; retry only with explicit consent."
	}
	return e
}

func atStage(stage string, err error) error {
	if err == nil {
		return nil
	}
	var e *Error
	if errors.As(err, &e) {
		copy := *e
		copy.Stage = stage
		return &copy
	}
	e = invalid("Operation failed; no additional model request was made.")
	e.Stage = stage
	if errors.Is(err, context.Canceled) {
		e.cause = context.Canceled
		e.Code = CodeTimeout
	} else if errors.Is(err, context.DeadlineExceeded) {
		e.cause = context.DeadlineExceeded
		e.Code = CodeTimeout
		e.Retryable = true
	}
	return e
}
