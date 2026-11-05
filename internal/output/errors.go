package output

import "errors"

// Kind classifies an error by who must act on it; each kind has its own exit code.
type Kind int

// Error kinds.
const (
	KindInternal Kind = iota
	KindInvalid
	KindForbidden
	KindNeedsHuman
)

// Exit codes of the agent contract.
const (
	ExitOK         = 0
	ExitInternal   = 1
	ExitInvalid    = 2
	ExitForbidden  = 3
	ExitNeedsHuman = 4
)

const internalFix = "retry the command; if it fails again, stop and show this error to a human"

// Error is an error shown to the agent: Fix says what to change and which command to call next.
type Error struct {
	Kind    Kind
	Code    string
	Message string
	Fix     string
	Err     error
	// Data and Text are the partial result of a command that did its work and still needs a human.
	Data any
	Text string
}

func (e *Error) Error() string { return e.Message }

func (e *Error) Unwrap() error { return e.Err }

// ExitCode maps the error kind to the process exit code.
func (e *Error) ExitCode() int {
	switch e.Kind {
	case KindInvalid:
		return ExitInvalid
	case KindForbidden:
		return ExitForbidden
	case KindNeedsHuman:
		return ExitNeedsHuman
	case KindInternal:
		return ExitInternal
	}
	return ExitInternal
}

// Invalid reports a bad artifact or input the agent can fix itself.
func Invalid(code, message, fix string) *Error {
	return &Error{Kind: KindInvalid, Code: code, Message: message, Fix: fix}
}

// Forbidden reports an action the policy does not allow.
func Forbidden(code, message, fix string) *Error {
	return &Error{Kind: KindForbidden, Code: code, Message: message, Fix: fix}
}

// NeedsHuman reports a step only a human can take.
func NeedsHuman(code, message, fix string) *Error {
	return &Error{Kind: KindNeedsHuman, Code: code, Message: message, Fix: fix}
}

// Internal wraps an unexpected failure.
func Internal(err error) *Error {
	return &Error{Kind: KindInternal, Code: "internal", Message: err.Error(), Fix: internalFix, Err: err}
}

// AsError returns err as *Error, treating any other error as Internal; nil stays nil.
func AsError(err error) *Error {
	if err == nil {
		return nil
	}
	if e, ok := errors.AsType[*Error](err); ok {
		return e
	}
	return Internal(err)
}

// ExitCode maps any error to the process exit code.
func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	return AsError(err).ExitCode()
}
