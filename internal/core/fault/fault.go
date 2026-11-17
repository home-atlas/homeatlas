// Package fault classifies application failures without HTTP status or database types.
package fault

import "errors"

type Kind uint8

const (
	Invalid Kind = iota + 1
	Unauthenticated
	Denied
	Missing
	Conflict
	Expired
	TooLarge
	Unsupported
	Locked
	Busy
	Internal
	Unavailable
)

type Error struct {
	Kind       Kind
	Code       string
	RetryAfter int
	Cause      error
}

func (e *Error) Error() string          { return e.Code }
func (e *Error) Unwrap() error          { return e.Cause }
func New(kind Kind, code string) *Error { return &Error{Kind: kind, Code: code} }
func Retry(kind Kind, code string, seconds int) *Error {
	return &Error{Kind: kind, Code: code, RetryAfter: seconds}
}
func From(err error) *Error {
	var f *Error
	if errors.As(err, &f) {
		return f
	}
	return &Error{Kind: Internal, Code: "operation_unavailable", Cause: err}
}
