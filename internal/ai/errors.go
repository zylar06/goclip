// Package ai implements bounded model requests and checkpointed analysis.
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
	CodeNoHighlights    = "no_highlights"
)

// Error contains only locally authored diagnostics. Neither provider bodies,
// credentials, prompts, URLs nor underlying network/filesystem errors are exposed.
// Retryable is informational. Analysis uses its own narrower, single-layer
// three-attempt policy for network/429/5xx; Complete and smoke never retry.
type Error struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Stage      string `json:"stage,omitempty"`
	HTTPStatus int    `json:"http_status,omitempty"`
	Retryable  bool   `json:"retryable"`
	cause      error
	// Set only by the transport boundary, never by model output.
	transientNetwork bool
	// refineRejected marks a validation failure authored by the visual boundary
	// review's own decode/validate step. It is the only failure class
	// AnalyzeVisual may drop and continue past, so cancellation, deadline, auth,
	// rate-limit, endpoint, checkpoint and progress-callback failures — which
	// never carry it — still abort the run.
	refineRejected bool
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

// markRefineRejection tags a provider-output validation failure so the visual
// pipeline can drop one unusable dense review instead of discarding an already
// billed analysis. Only invalid_response is tagged: a no-highlights result, a
// transport/auth/rate-limit failure, a context error and a progress-callback
// failure all pass through untagged and still abort. nil stays nil.
func markRefineRejection(err error) error {
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeInvalidResponse {
		return err
	}
	tagged := *e
	tagged.refineRejected = true
	return &tagged
}

// rejectedRefinement reports whether err is a tagged refine-output validation
// failure. atStage copies the concrete *Error, so the tag survives staging.
func rejectedRefinement(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == CodeInvalidResponse && e.refineRejected
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
