package resolve

import (
	"errors"
	"fmt"
)

type ErrorCategory string

const (
	ErrAuth           ErrorCategory = "auth"
	ErrRateLimit      ErrorCategory = "rate_limit"
	ErrUnavailable    ErrorCategory = "unavailable"
	ErrUnsupported    ErrorCategory = "unsupported_url"
	ErrResolveFailure ErrorCategory = "resolve_failure"
	ErrTransfer       ErrorCategory = "transfer_failure"
)

type Error struct {
	Category ErrorCategory `json:"category"`
	Message  string        `json:"message"`
	Cause    error         `json:"-"`
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("[%s] %s: %v", e.Category, e.Message, e.Cause)
	}
	return fmt.Sprintf("[%s] %s", e.Category, e.Message)
}

func (e *Error) Unwrap() error { return e.Cause }

func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	if !ok {
		return false
	}
	return e.Category == t.Category || (t.Category == "" && errors.Is(e.Cause, target))
}

func NewError(category ErrorCategory, message string, cause error) *Error {
	return &Error{Category: category, Message: message, Cause: cause}
}
