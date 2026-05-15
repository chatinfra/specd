package apierr

import "fmt"

type Error struct {
	Status  int            `json:"-"`
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
	Cause   error          `json:"-"`
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func New(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

func Errorf(status int, code, format string, args ...any) *Error {
	return New(status, code, fmt.Sprintf(format, args...))
}

func Wrap(status int, code, message string, err error) *Error {
	return &Error{Status: status, Code: code, Message: message, Cause: err}
}

func WithDetails(err *Error, details map[string]any) *Error {
	if err == nil {
		return nil
	}
	err.Details = details
	return err
}

func From(err error) *Error {
	if err == nil {
		return nil
	}
	if typed, ok := err.(*Error); ok {
		return typed
	}
	return Wrap(500, "internal_error", "internal server error", err)
}
