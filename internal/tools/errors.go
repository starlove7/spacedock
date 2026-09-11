package tools

import (
	"errors"
	"fmt"
	"strings"
)

type Error struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Category  string         `json:"category"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details,omitempty"`
}

func (e *Error) Error() string { return e.Message }
func New(code, message, category string, retryable bool, details map[string]any) *Error {
	return &Error{Code: code, Message: message, Category: category, Retryable: retryable, Details: details}
}
func Validation(err error) error {
	return New("VALIDATION_ERROR", safeMessage(err), "validation", false, nil)
}
func Denied(p, w string) error {
	return New("PERMISSION_DENIED", "permission required", "permission", false, map[string]any{"permission": p, "workspace_id": w})
}
func SensitivePathDenied() error {
	return New("PERMISSION_DENIED", "sensitive path access denied", "permission", false, map[string]any{"reason": "sensitive_path"})
}
func NotFound(err error) error { return New("NOT_FOUND", safeMessage(err), "not_found", false, nil) }
func Conflict(err error) error { return New("CONFLICT", safeMessage(err), "conflict", false, nil) }
func Execution(err error) error {
	return New("EXECUTION_ERROR", safeMessage(err), "execution", true, nil)
}
func Internal(err error) error { return New("INTERNAL", safeMessage(err), "internal", false, nil) }
func safeMessage(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if strings.Contains(strings.ToLower(msg), "private key") {
		return "operation failed"
	}
	return msg
}
func Wrap(err error) error {
	if err == nil {
		return nil
	}
	var te *Error
	if errors.As(err, &te) {
		return te
	}
	return New("INTERNAL", "internal error", "internal", false, nil)
}

var _ = fmt.Sprintf
